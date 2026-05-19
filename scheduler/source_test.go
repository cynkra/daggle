package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cynkra/daggle/state"
)

// TestScheduler_PersistsTriggerSource verifies that the trigger source
// (the string passed to triggerRun) lands in both meta.json and the
// run_started event line. This is the only on-disk evidence of what
// fired a run — see PR for the visibility-gap context.
func TestScheduler_PersistsTriggerSource(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0o755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	writeDAG(t, dagDir, "fast.yaml", `
name: fast-dag
trigger:
  schedule: "@every 1h"
steps:
  - id: ok
    command: "true"
`)

	sched := New([]state.DAGSource{{Name: "test", Dir: dagDir}})
	if err := sched.syncDAGs(context.Background()); err != nil {
		t.Fatalf("syncDAGs: %v", err)
	}

	dagPath := filepath.Join(dagDir, "fast.yaml")
	sched.triggerRun(dagPath, "cron")

	// Wait for the run to complete.
	var run *state.RunInfo
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := state.LatestRun("fast-dag")
		if err == nil && r != nil && state.RunStatus(r.Dir) == "completed" {
			run = r
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if run == nil {
		t.Fatal("expected a completed run for fast-dag, got none")
	}

	// meta.json should carry trigger_source.
	meta, err := state.ReadMeta(run.Dir)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta.TriggerSource != "cron" {
		t.Errorf("meta.TriggerSource = %q, want %q", meta.TriggerSource, "cron")
	}

	// run_started event should carry source.
	events, err := state.ReadEvents(run.Dir)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no events recorded")
	}
	first := events[0]
	if first.Type != state.EventRunStarted {
		t.Fatalf("first event = %q, want %q", first.Type, state.EventRunStarted)
	}
	if first.Source != "cron" {
		t.Errorf("run_started.Source = %q, want %q", first.Source, "cron")
	}
}

// TestScheduler_PreLaunchFailureCarriesSource verifies that synthetic
// run_started events written by writeRunFailureEvents (parse/env-resolve
// failures, engine init failures) include the trigger source. Without
// this, trigger-time failures show up as "unknown origin" in the UI.
func TestScheduler_PreLaunchFailureCarriesSource(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0o755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	// Env reference to a nonexistent var → ResolveEnv fails before engine handoff.
	writeDAG(t, dagDir, "broken.yaml", `
name: broken-src
trigger:
  schedule: "@every 1h"
env:
  TOKEN: ${env:DAGGLE_TEST_DOES_NOT_EXIST}
steps:
  - id: noop
    command: echo hi
`)

	sched := New([]state.DAGSource{{Name: "test", Dir: dagDir}})
	sched.triggerRun(filepath.Join(dagDir, "broken.yaml"), "webhook")

	deadline := time.Now().Add(2 * time.Second)
	var run *state.RunInfo
	for time.Now().Before(deadline) {
		r, err := state.LatestRun("broken-src")
		if err == nil && r != nil {
			run = r
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if run == nil {
		t.Fatal("expected a recorded run after env-resolve failure")
	}

	events, err := state.ReadEvents(run.Dir)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("expected >=2 events (run_started + run_failed), got %d", len(events))
	}
	if events[0].Type != state.EventRunStarted {
		t.Fatalf("events[0].Type = %q, want %q", events[0].Type, state.EventRunStarted)
	}
	if events[0].Source != "webhook" {
		t.Errorf("events[0].Source = %q, want %q", events[0].Source, "webhook")
	}
}
