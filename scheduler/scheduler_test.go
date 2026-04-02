package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPIDFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	// Should not be running initially
	if IsRunning() {
		t.Error("expected IsRunning() = false before writing PID")
	}

	// Write PID
	if err := WritePID(); err != nil {
		t.Fatalf("WritePID: %v", err)
	}

	// Should detect as running (our own PID)
	if !IsRunning() {
		t.Error("expected IsRunning() = true after writing PID")
	}

	// Read PID
	pid, err := ReadPID()
	if err != nil {
		t.Fatalf("ReadPID: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("PID = %d, want %d", pid, os.Getpid())
	}

	// Remove PID
	if err := RemovePID(); err != nil {
		t.Fatalf("RemovePID: %v", err)
	}

	if IsRunning() {
		t.Error("expected IsRunning() = false after removing PID")
	}
}

func TestScheduler_ScanDAGs(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	// Create a DAG with a schedule
	writeDAG(t, dagDir, "scheduled.yaml", `
name: scheduled-dag
trigger:
  schedule: "@every 1h"
steps:
  - id: hello
    command: echo hello
`)

	// Create a DAG without a schedule
	writeDAG(t, dagDir, "manual.yaml", `
name: manual-dag
steps:
  - id: hello
    command: echo hello
`)

	sched := New(dagDir)
	if err := sched.syncDAGs(context.Background()); err != nil {
		t.Fatalf("syncDAGs: %v", err)
	}

	// Only the scheduled DAG should be registered
	if len(sched.registered) != 1 {
		t.Errorf("registered = %d, want 1", len(sched.registered))
	}
	if _, ok := sched.registered["scheduled-dag"]; !ok {
		t.Error("expected 'scheduled-dag' to be registered")
	}
}

func TestScheduler_HotReload(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	sched := New(dagDir)

	// Initial scan — empty
	_ = sched.syncDAGs(context.Background())
	if len(sched.registered) != 0 {
		t.Errorf("registered = %d, want 0", len(sched.registered))
	}

	// Add a DAG
	writeDAG(t, dagDir, "new.yaml", `
name: new-dag
trigger:
  schedule: "@every 1h"
steps:
  - id: a
    command: echo a
`)
	_ = sched.syncDAGs(context.Background())
	if len(sched.registered) != 1 {
		t.Errorf("after add: registered = %d, want 1", len(sched.registered))
	}

	// Update schedule
	writeDAG(t, dagDir, "new.yaml", `
name: new-dag
trigger:
  schedule: "@every 2h"
steps:
  - id: a
    command: echo a
`)
	_ = sched.syncDAGs(context.Background())
	if sched.registered["new-dag"].schedule != "@every 2h" {
		t.Errorf("schedule = %q, want %q", sched.registered["new-dag"].schedule, "@every 2h")
	}

	// Remove DAG file
	_ = os.Remove(filepath.Join(dagDir, "new.yaml"))
	_ = sched.syncDAGs(context.Background())
	if len(sched.registered) != 0 {
		t.Errorf("after remove: registered = %d, want 0", len(sched.registered))
	}
}

func TestScheduler_SkipOverlap(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	// Create a DAG that sleeps
	writeDAG(t, dagDir, "slow.yaml", `
name: slow-dag
trigger:
  schedule: "@every 1s"
steps:
  - id: slow
    command: sleep 10
`)

	sched := New(dagDir)
	_ = sched.syncDAGs(context.Background())

	// Trigger first run
	dagPath := filepath.Join(dagDir, "slow.yaml")
	sched.triggerRun(dagPath, "test")

	// Give it a moment to start
	time.Sleep(100 * time.Millisecond)

	// Verify it's running
	sched.mu.Lock()
	running := len(sched.running)
	sched.mu.Unlock()
	if running != 1 {
		t.Fatalf("running = %d, want 1", running)
	}

	// Trigger again — should be skipped
	sched.triggerRun(dagPath, "test")

	sched.mu.Lock()
	running = len(sched.running)
	sched.mu.Unlock()
	if running != 1 {
		t.Errorf("after overlap: running = %d, want 1 (should have skipped)", running)
	}

	// Clean up: cancel the running DAG
	sched.mu.Lock()
	for _, cancel := range sched.running {
		cancel()
	}
	sched.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
}

func TestScheduler_MaxConcurrent(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	sched := New(dagDir)
	sched.maxConcurrent = 2

	// Create multiple DAGs
	for _, name := range []string{"dag-a", "dag-b", "dag-c"} {
		writeDAG(t, dagDir, name+".yaml", `
name: `+name+`
trigger:
  schedule: "@every 1h"
steps:
  - id: slow
    command: sleep 30
`)
	}
	_ = sched.syncDAGs(context.Background())

	// Trigger all three
	sched.triggerRun(filepath.Join(dagDir, "dag-a.yaml"), "test")
	sched.triggerRun(filepath.Join(dagDir, "dag-b.yaml"), "test")
	time.Sleep(100 * time.Millisecond)

	sched.triggerRun(filepath.Join(dagDir, "dag-c.yaml"), "test")
	time.Sleep(100 * time.Millisecond)

	sched.mu.Lock()
	running := len(sched.running)
	sched.mu.Unlock()

	// Only 2 should be running (max concurrent = 2)
	if running != 2 {
		t.Errorf("running = %d, want 2 (max concurrent)", running)
	}

	// Clean up
	sched.mu.Lock()
	for _, cancel := range sched.running {
		cancel()
	}
	sched.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
}

func TestScheduler_StartStop(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	sched := New(dagDir)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- sched.Start(ctx)
	}()

	// Let it run briefly
	time.Sleep(100 * time.Millisecond)

	// Stop
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not stop within 5 seconds")
	}
}

func TestScheduler_WatchTrigger(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	watchDir := filepath.Join(tmpDir, "data")
	_ = os.MkdirAll(dagDir, 0755)
	_ = os.MkdirAll(watchDir, 0755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	// Create a DAG with a watch trigger (slow so we can catch it running)
	writeDAG(t, dagDir, "watcher.yaml", `
name: watcher-dag
trigger:
  watch:
    path: `+watchDir+`
    pattern: "*.csv"
    debounce: 100ms
steps:
  - id: process
    command: sleep 10
`)

	sched := New(dagDir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := sched.syncDAGs(ctx); err != nil {
		t.Fatalf("syncDAGs: %v", err)
	}

	// DAG should be registered
	if _, ok := sched.registered["watcher-dag"]; !ok {
		t.Fatal("expected 'watcher-dag' to be registered")
	}

	// Write a matching file — should trigger a run after debounce
	if err := os.WriteFile(filepath.Join(watchDir, "data.csv"), []byte("a,b\n1,2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for debounce + run start
	time.Sleep(500 * time.Millisecond)

	sched.mu.Lock()
	running := len(sched.running)
	sched.mu.Unlock()

	if running != 1 {
		t.Errorf("running = %d, want 1 (watch trigger should have fired)", running)
	}

	// Clean up
	sched.mu.Lock()
	for _, cancelFn := range sched.running {
		cancelFn()
	}
	sched.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
}

func TestScheduler_WatchTriggerPatternFilter(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	watchDir := filepath.Join(tmpDir, "data")
	_ = os.MkdirAll(dagDir, 0755)
	_ = os.MkdirAll(watchDir, 0755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	writeDAG(t, dagDir, "watcher.yaml", `
name: watcher-dag
trigger:
  watch:
    path: `+watchDir+`
    pattern: "*.csv"
    debounce: 100ms
steps:
  - id: process
    command: echo processing
`)

	sched := New(dagDir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = sched.syncDAGs(ctx)

	// Write a non-matching file — should NOT trigger
	if err := os.WriteFile(filepath.Join(watchDir, "data.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(500 * time.Millisecond)

	sched.mu.Lock()
	running := len(sched.running)
	sched.mu.Unlock()

	if running != 0 {
		t.Errorf("running = %d, want 0 (non-matching file should not trigger)", running)
	}
}

func writeDAG(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("write DAG %s: %v", name, err)
	}
}
