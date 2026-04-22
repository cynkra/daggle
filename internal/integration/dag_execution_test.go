//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cynkra/daggle/state"
)

// TestLinearShellDAG exercises the happy path: three shell steps in a chain,
// events written in the expected order, log files exist for each step.
func TestLinearShellDAG(t *testing.T) {
	isolate(t)
	d := loadDAG(t, "linear.yaml", nil)

	run, err := runDAG(t, d)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	events := readEvents(t, run)
	types := eventTypes(events)

	// Must start with run_started and end with run_completed.
	if len(types) == 0 || types[0] != state.EventRunStarted {
		t.Fatalf("first event = %v, want %s", types, state.EventRunStarted)
	}
	if types[len(types)-1] != state.EventRunCompleted {
		t.Errorf("last event = %q, want %s", types[len(types)-1], state.EventRunCompleted)
	}

	// Each step must have exactly one started + one completed event, in order.
	for _, stepID := range []string{"a", "b", "c"} {
		started := findEvent(events, state.EventStepStarted, stepID)
		completed := findEvent(events, state.EventStepCompleted, stepID)
		if started == nil {
			t.Errorf("missing step_started for %q", stepID)
			continue
		}
		if completed == nil {
			t.Errorf("missing step_completed for %q", stepID)
			continue
		}
		if !completed.Timestamp.After(started.Timestamp) && !completed.Timestamp.Equal(started.Timestamp) {
			t.Errorf("%s: completed (%v) before started (%v)", stepID, completed.Timestamp, started.Timestamp)
		}
	}

	// Log files must exist for every step.
	for _, stepID := range []string{"a", "b", "c"} {
		for _, suffix := range []string{".stdout.log", ".stderr.log"} {
			p := filepath.Join(run.Dir, stepID+suffix)
			if _, err := os.Stat(p); err != nil {
				t.Errorf("missing log file %s: %v", p, err)
			}
		}
	}
}

// TestParallelTierOutputPassing verifies that outputs emitted by two
// parallel steps are both injected into the downstream step's env, and
// that the marker parser / env-injection glue works end-to-end.
func TestParallelTierOutputPassing(t *testing.T) {
	isolate(t)
	d := loadDAG(t, "diamond.yaml", nil)

	if _, err := runDAG(t, d); err != nil {
		t.Fatalf("run failed: %v", err)
	}
}

// TestRPipeline runs real Rscript subprocesses and verifies outputs flow
// between R steps and meta.json captures reproducibility fields.
func TestRPipeline(t *testing.T) {
	requireR(t)
	isolate(t)
	d := loadDAG(t, "r_pipeline.yaml", nil)

	run, err := runDAG(t, d)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	// Success should not have produced a sessioninfo.json — that's only on R failure.
	sessionInfo := filepath.Join(run.Dir, "consume.sessioninfo.json")
	if _, err := os.Stat(sessionInfo); !os.IsNotExist(err) {
		t.Errorf("consume.sessioninfo.json should not exist on success (err=%v)", err)
	}

	// meta.json must capture the current platform at minimum.
	meta, err := state.ReadMeta(run.Dir)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta.Platform == "" {
		t.Error("meta.Platform is empty")
	}
	if meta.Status != "completed" {
		t.Errorf("meta.Status = %q, want completed", meta.Status)
	}

	// The consume step must have captured and echoed the upstream output.
	stdout, err := os.ReadFile(filepath.Join(run.Dir, "consume.stdout.log"))
	if err != nil {
		t.Fatalf("read consume stdout: %v", err)
	}
	if !strings.Contains(string(stdout), "consumed 7") {
		t.Errorf("consume stdout missing 'consumed 7': %s", stdout)
	}
}

// TestRetryBackoff confirms the engine retries a failing step and emits
// step_retrying events between attempts. A linear backoff of 1s+2s means
// the total runtime must be at least ~3 seconds.
func TestRetryBackoff(t *testing.T) {
	isolate(t)
	d := loadDAG(t, "retry.yaml", nil)

	start := time.Now()
	run, err := runDAG(t, d)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	// 1s + 2s backoff before attempts 2 and 3 = ≥3s total.
	if elapsed < 3*time.Second {
		t.Errorf("elapsed = %v, expected >= 3s (linear backoff 1s+2s)", elapsed)
	}

	events := readEvents(t, run)
	retries := countEvents(events, state.EventStepRetrying)
	if retries != 2 {
		t.Errorf("step_retrying events = %d, want 2", retries)
	}
	if findEvent(events, state.EventStepCompleted, "flaky") == nil {
		t.Error("missing final step_completed for flaky")
	}
}

// TestTimeoutEnforcement asserts that a step exceeding its timeout is
// killed promptly (within ~10s for a 2s timeout + 5s grace), the run
// ends in failure, and a step_failed event is recorded.
func TestTimeoutEnforcement(t *testing.T) {
	isolate(t)
	d := loadDAG(t, "timeout.yaml", nil)

	// Outer deadline so a hung subprocess can't wedge the whole suite.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	run, err := runDAGCtx(t, ctx, d)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected run to fail due to timeout")
	}
	// 2s timeout + 5s SIGTERM→SIGKILL grace window, plus a little slack.
	if elapsed > 15*time.Second {
		t.Errorf("elapsed = %v, want < 15s (timeout should kill process promptly)", elapsed)
	}

	events := readEvents(t, run)
	if findEvent(events, state.EventStepFailed, "slow") == nil {
		t.Error("missing step_failed for slow")
	}
	if findEvent(events, state.EventRunFailed, "") == nil {
		t.Error("missing run_failed event")
	}
}

// TestHooksOnSuccessAndExit verifies on_success + on_exit hooks run when
// the DAG succeeds, and on_failure does not.
func TestHooksOnSuccessAndExit(t *testing.T) {
	isolate(t)
	hooksDir := t.TempDir()
	d := loadDAG(t, "hooks.yaml", map[string]string{"hooks_dir": hooksDir})

	if _, err := runDAG(t, d); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	for _, marker := range []string{"on_success.marker", "on_exit.marker"} {
		if _, err := os.Stat(filepath.Join(hooksDir, marker)); err != nil {
			t.Errorf("expected marker %s: %v", marker, err)
		}
	}
	if _, err := os.Stat(filepath.Join(hooksDir, "on_failure.marker")); !os.IsNotExist(err) {
		t.Errorf("on_failure.marker should not exist on success (err=%v)", err)
	}
}

// TestHooksOnFailureAndExit verifies on_failure + on_exit run when the
// DAG fails, and on_success does not.
func TestHooksOnFailureAndExit(t *testing.T) {
	isolate(t)
	hooksDir := t.TempDir()
	d := loadDAG(t, "hooks_fail.yaml", map[string]string{"hooks_dir": hooksDir})

	if _, err := runDAG(t, d); err == nil {
		t.Fatal("expected run to fail")
	}

	for _, marker := range []string{"on_failure.marker", "on_exit.marker"} {
		if _, err := os.Stat(filepath.Join(hooksDir, marker)); err != nil {
			t.Errorf("expected marker %s: %v", marker, err)
		}
	}
	if _, err := os.Stat(filepath.Join(hooksDir, "on_success.marker")); !os.IsNotExist(err) {
		t.Errorf("on_success.marker should not exist on failure (err=%v)", err)
	}
}

// TestWhenSkip verifies a step with a false `when` condition emits
// step_skipped, and downstream steps that depend on the skipped step's
// peer still run.
func TestWhenSkip(t *testing.T) {
	isolate(t)
	d := loadDAG(t, "when.yaml", nil)

	run, err := runDAG(t, d)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	events := readEvents(t, run)

	if findEvent(events, "step_skipped", "skipped") == nil {
		t.Error("missing step_skipped event for 'skipped'")
	}
	if findEvent(events, state.EventStepCompleted, "after") == nil {
		t.Error("downstream step 'after' did not complete")
	}
	if findEvent(events, state.EventStepStarted, "skipped") != nil {
		t.Error("'skipped' step should not have emitted step_started")
	}
}

// TestEventSchemaStability is the schema-drift canary. It runs a simple
// DAG and asserts the event stream matches the documented shape: every
// event has Version, Timestamp, Type; step events have StepID; run/step
// terminal events have the expected types in the expected positions.
func TestEventSchemaStability(t *testing.T) {
	isolate(t)
	d := loadDAG(t, "linear.yaml", nil)

	run, err := runDAG(t, d)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	events := readEvents(t, run)

	// Every event must carry version, a non-zero timestamp, and a type.
	for i, ev := range events {
		if ev.Version == 0 {
			t.Errorf("event[%d] has Version=0", i)
		}
		if ev.Timestamp.IsZero() {
			t.Errorf("event[%d] has zero Timestamp", i)
		}
		if ev.Type == "" {
			t.Errorf("event[%d] has empty Type", i)
		}
	}

	// Expected shape: run_started, (step_started, step_completed)*3, run_completed.
	wantTypes := []string{
		state.EventRunStarted,
		state.EventStepStarted, state.EventStepCompleted,
		state.EventStepStarted, state.EventStepCompleted,
		state.EventStepStarted, state.EventStepCompleted,
		state.EventRunCompleted,
	}
	gotTypes := eventTypes(events)
	if len(gotTypes) != len(wantTypes) {
		t.Fatalf("event count = %d, want %d: %v", len(gotTypes), len(wantTypes), gotTypes)
	}
	for i, want := range wantTypes {
		if gotTypes[i] != want {
			t.Errorf("event[%d] type = %q, want %q", i, gotTypes[i], want)
		}
	}

	// step_completed events must include StepID, Duration, and a non-zero Attempt.
	for _, ev := range events {
		if ev.Type != state.EventStepCompleted {
			continue
		}
		if ev.StepID == "" {
			t.Error("step_completed missing StepID")
		}
		if ev.Duration == "" {
			t.Error("step_completed missing Duration")
		}
		if ev.Attempt == 0 {
			t.Error("step_completed missing Attempt")
		}
	}
}
