package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cynkra/daggle/state"
)

func TestNewCronEntry_ParsesExpression(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	ce, err := newCronEntry("30 9 * * 2", "/some/path.yaml", nil, now)
	if err != nil {
		t.Fatalf("newCronEntry: %v", err)
	}
	if ce.expr != "30 9 * * 2" {
		t.Errorf("expr = %q, want %q", ce.expr, "30 9 * * 2")
	}
	if ce.next.IsZero() {
		t.Errorf("next should be set")
	}
	if !ce.next.After(now) {
		t.Errorf("next %v should be after now %v", ce.next, now)
	}
}

func TestNewCronEntry_InvalidExpressionErrors(t *testing.T) {
	_, err := newCronEntry("not-a-cron-expression", "/x", nil, time.Now())
	if err == nil {
		t.Fatal("expected error for invalid cron expression")
	}
}

// TestFireDueCronEntries_AdvancesNextPastNow is the bug-regression test. With
// the old timer-based executor a daemon that drifted across multiple ticks
// would silently miss them and never advance: next stayed at some long-stale
// past value. The polling executor must advance next past now in a single
// call, no matter how many ticks were missed. It must also actually fire at
// least one run (the overlap=skip default dedupes the rest into one effective
// catch-up — that's the intended semantic when `catchup` is off, and verified
// in TestFireDueCronEntries_OverlapSkipDedupes).
func TestFireDueCronEntries_AdvancesNextPastNow(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0o755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	writeDAG(t, dagDir, "missed.yaml", `
name: missed-ticks
trigger:
  schedule: "@every 1m"
steps:
  - id: quick
    command: "true"
`)

	sched := New([]state.DAGSource{{Name: "test", Dir: dagDir}})
	if err := sched.syncDAGs(context.Background()); err != nil {
		t.Fatalf("syncDAGs: %v", err)
	}

	// Force the entry's next time 5 minutes into the past. Calling
	// fireDueCronEntries(now) must advance next past now (verified by the
	// internal loop in the executor), regardless of how many ticks fall in
	// that interval.
	sched.mu.Lock()
	ce := sched.registered["missed-ticks"].cron
	if ce == nil {
		sched.mu.Unlock()
		t.Fatal("missed-ticks not registered with cron")
	}
	now := time.Now()
	ce.next = now.Add(-5*time.Minute - time.Second)
	sched.mu.Unlock()

	sched.fireDueCronEntries(now)

	sched.mu.Lock()
	advancedNext := sched.registered["missed-ticks"].cron.next
	sched.mu.Unlock()
	if !advancedNext.After(now) {
		t.Errorf("advanced next = %v, must be after now = %v", advancedNext, now)
	}

	// At least one run must have been created. Number is governed by overlap
	// policy (default skip), not by the executor — see the dedicated test.
	// Wait for the run to also FINISH so the temp dir is quiescent when the
	// test cleanup deletes it; otherwise the in-flight executor goroutine
	// crashes trying to write step logs into a deleted directory.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sched.mu.Lock()
		running := len(sched.running)
		sched.mu.Unlock()
		runs, _ := state.ListRuns("missed-ticks")
		if running == 0 && len(runs) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	runs, err := state.ListRuns("missed-ticks")
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) < 1 {
		t.Errorf("len(runs) = %d, want >= 1 (missed-tick catchup did not fire)", len(runs))
	}
}

// TestFireDueCronEntries_OverlapSkipDedupes verifies the default semantic
// when many ticks are missed: with overlap=skip (the default), the executor
// fires triggerRun for every missed tick but the overlap policy collapses
// them into one effective run. Users who want every missed tick to actually
// execute should opt into `catchup: all`.
func TestFireDueCronEntries_OverlapSkipDedupes(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0o755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	// Use a sleeping step so the first run is still in-flight while the
	// executor's loop fires the rest — that's what triggers the skip.
	writeDAG(t, dagDir, "skip.yaml", `
name: skip-dedupe
trigger:
  schedule: "@every 1m"
steps:
  - id: slow
    command: sleep 1
`)

	sched := New([]state.DAGSource{{Name: "test", Dir: dagDir}})
	if err := sched.syncDAGs(context.Background()); err != nil {
		t.Fatalf("syncDAGs: %v", err)
	}

	sched.mu.Lock()
	sched.registered["skip-dedupe"].cron.next = time.Now().Add(-5*time.Minute - time.Second)
	sched.mu.Unlock()

	sched.fireDueCronEntries(time.Now())

	// Wait for the (single) run to settle, then assert count.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sched.mu.Lock()
		running := len(sched.running)
		sched.mu.Unlock()
		runs, _ := state.ListRuns("skip-dedupe")
		if running == 0 && len(runs) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	runs, _ := state.ListRuns("skip-dedupe")
	if len(runs) != 1 {
		t.Errorf("len(runs) = %d, want exactly 1 (overlap=skip should have deduped)", len(runs))
	}
}

func TestFireDueCronEntries_NotYetDueDoesNothing(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0o755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	writeDAG(t, dagDir, "future.yaml", `
name: future-only
trigger:
  schedule: "@every 1h"
steps:
  - id: quick
    command: "true"
`)

	sched := New([]state.DAGSource{{Name: "test", Dir: dagDir}})
	if err := sched.syncDAGs(context.Background()); err != nil {
		t.Fatalf("syncDAGs: %v", err)
	}

	now := time.Now()
	// next is well into the future after registration; fireDueCronEntries
	// must not fire anything.
	sched.fireDueCronEntries(now)
	time.Sleep(200 * time.Millisecond)

	runs, _ := state.ListRuns("future-only")
	if len(runs) != 0 {
		t.Errorf("len(runs) = %d, want 0 (no tick should have fired)", len(runs))
	}
}

func TestFireDueCronEntries_OnlyDueEntriesFire(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0o755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	writeDAG(t, dagDir, "due.yaml", `
name: due-dag
trigger:
  schedule: "@every 1m"
steps:
  - id: quick
    command: "true"
`)
	writeDAG(t, dagDir, "future.yaml", `
name: not-due-dag
trigger:
  schedule: "@every 1h"
steps:
  - id: quick
    command: "true"
`)

	sched := New([]state.DAGSource{{Name: "test", Dir: dagDir}})
	if err := sched.syncDAGs(context.Background()); err != nil {
		t.Fatalf("syncDAGs: %v", err)
	}

	now := time.Now()
	sched.mu.Lock()
	sched.registered["due-dag"].cron.next = now.Add(-30 * time.Second)
	// not-due-dag.next is computed during registration as now + ~1h; leave
	// it where it is to verify it stays untouched.
	notDueOriginal := sched.registered["not-due-dag"].cron.next
	sched.mu.Unlock()

	sched.fireDueCronEntries(now)

	// Wait for the due-dag run to complete so the temp dir is quiescent
	// before t.Cleanup deletes it.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sched.mu.Lock()
		running := len(sched.running)
		sched.mu.Unlock()
		if running == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	dueRuns, _ := state.ListRuns("due-dag")
	notDueRuns, _ := state.ListRuns("not-due-dag")
	if len(dueRuns) != 1 {
		t.Errorf("due-dag runs = %d, want 1", len(dueRuns))
	}
	if len(notDueRuns) != 0 {
		t.Errorf("not-due-dag runs = %d, want 0", len(notDueRuns))
	}

	sched.mu.Lock()
	if sched.registered["not-due-dag"].cron.next != notDueOriginal {
		t.Errorf("not-due-dag.next changed: was %v, now %v", notDueOriginal, sched.registered["not-due-dag"].cron.next)
	}
	sched.mu.Unlock()
}
