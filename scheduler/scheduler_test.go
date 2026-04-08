package scheduler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	sched := New([]DAGSource{{Name: "test", Dir: dagDir}})
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

	sched := New([]DAGSource{{Name: "test", Dir: dagDir}})

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

	sched := New([]DAGSource{{Name: "test", Dir: dagDir}})
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
	for _, entry := range sched.running {
		entry.cancel()
	}
	sched.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
}

func TestScheduler_CancelOverlap(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	// DAG with overlap: cancel policy
	writeDAG(t, dagDir, "cancel.yaml", `
name: cancel-dag
trigger:
  schedule: "@every 1s"
  overlap: cancel
steps:
  - id: slow
    command: sleep 30
`)

	sched := New([]DAGSource{{Name: "test", Dir: dagDir}})
	_ = sched.syncDAGs(context.Background())

	dagPath := filepath.Join(dagDir, "cancel.yaml")

	// Start first run
	sched.triggerRun(dagPath, "test")
	time.Sleep(200 * time.Millisecond)

	sched.mu.Lock()
	running := len(sched.running)
	sched.mu.Unlock()
	if running != 1 {
		t.Fatalf("running = %d, want 1", running)
	}

	// Trigger again — should cancel old and start new (overlap: cancel)
	sched.triggerRun(dagPath, "test")
	time.Sleep(500 * time.Millisecond)

	sched.mu.Lock()
	running = len(sched.running)
	sched.mu.Unlock()

	// Should still have exactly 1 running (the new one replaced the old)
	if running != 1 {
		t.Errorf("after cancel overlap: running = %d, want 1", running)
	}

	// Clean up
	sched.mu.Lock()
	for _, entry := range sched.running {
		entry.cancel()
	}
	sched.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
}

func TestScheduler_MaxConcurrent(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	sched := New([]DAGSource{{Name: "test", Dir: dagDir}})
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
	for _, entry := range sched.running {
		entry.cancel()
	}
	sched.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
}

func TestScheduler_StartStop(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	sched := New([]DAGSource{{Name: "test", Dir: dagDir}})

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

	sched := New([]DAGSource{{Name: "test", Dir: dagDir}})
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
	for _, entry := range sched.running {
		entry.cancel()
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

	sched := New([]DAGSource{{Name: "test", Dir: dagDir}})
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

func TestScheduler_OnDAGTrigger(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	// Upstream DAG: fast, cron-triggered
	writeDAG(t, dagDir, "upstream.yaml", `
name: upstream
trigger:
  schedule: "@every 1h"
steps:
  - id: produce
    command: echo upstream-done
`)

	// Downstream DAG: triggered when upstream completes
	writeDAG(t, dagDir, "downstream.yaml", `
name: downstream
trigger:
  on_dag:
    name: upstream
    status: completed
steps:
  - id: consume
    command: sleep 10
`)

	sched := New([]DAGSource{{Name: "test", Dir: dagDir}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = sched.syncDAGs(ctx)

	// Start the completion dispatcher
	go sched.dispatchCompletions(ctx)

	// Verify on_dag listener is registered
	sched.mu.Lock()
	listeners := sched.onDAGListeners["upstream"]
	sched.mu.Unlock()
	if len(listeners) != 1 {
		t.Fatalf("expected 1 on_dag listener for upstream, got %d", len(listeners))
	}

	// Manually trigger the upstream DAG
	sched.triggerRun(filepath.Join(dagDir, "upstream.yaml"), "test")

	// Wait for upstream to complete and on_dag to fire
	time.Sleep(1 * time.Second)

	sched.mu.Lock()
	running := len(sched.running)
	sched.mu.Unlock()

	// Downstream should now be running (triggered by upstream completion)
	if running != 1 {
		t.Errorf("running = %d, want 1 (downstream should be triggered by on_dag)", running)
	}

	// Clean up
	sched.mu.Lock()
	for _, entry := range sched.running {
		entry.cancel()
	}
	sched.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
}

func TestScheduler_OnDAGTriggerStatusFilter(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	// Upstream DAG: succeeds
	writeDAG(t, dagDir, "upstream.yaml", `
name: upstream
trigger:
  schedule: "@every 1h"
steps:
  - id: ok
    command: echo ok
`)

	// Downstream: only triggers on failure
	writeDAG(t, dagDir, "on-fail.yaml", `
name: on-fail
trigger:
  on_dag:
    name: upstream
    status: failed
steps:
  - id: alert
    command: sleep 10
`)

	sched := New([]DAGSource{{Name: "test", Dir: dagDir}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = sched.syncDAGs(ctx)
	go sched.dispatchCompletions(ctx)

	// Trigger upstream (will succeed)
	sched.triggerRun(filepath.Join(dagDir, "upstream.yaml"), "test")

	// Wait for upstream to complete
	time.Sleep(1 * time.Second)

	sched.mu.Lock()
	running := len(sched.running)
	sched.mu.Unlock()

	// on-fail should NOT have triggered (upstream succeeded, listener wants "failed")
	if running != 0 {
		t.Errorf("running = %d, want 0 (on-fail should not trigger on success)", running)
	}
}

func TestScheduler_ConditionTrigger(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	flagFile := filepath.Join(tmpDir, "ready.flag")
	_ = os.MkdirAll(dagDir, 0755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	// DAG with condition trigger: fires when flag file exists
	writeDAG(t, dagDir, "conditional.yaml", `
name: conditional
trigger:
  condition:
    command: test -f `+flagFile+`
    poll_interval: 200ms
steps:
  - id: process
    command: sleep 10
`)

	sched := New([]DAGSource{{Name: "test", Dir: dagDir}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = sched.syncDAGs(ctx)

	// No flag file yet — should not trigger
	time.Sleep(500 * time.Millisecond)
	sched.mu.Lock()
	running := len(sched.running)
	sched.mu.Unlock()
	if running != 0 {
		t.Fatalf("running = %d, want 0 (condition not met yet)", running)
	}

	// Create flag file — should trigger on next poll
	_ = os.WriteFile(flagFile, []byte("ready"), 0644)
	time.Sleep(500 * time.Millisecond)

	sched.mu.Lock()
	running = len(sched.running)
	sched.mu.Unlock()
	if running != 1 {
		t.Errorf("running = %d, want 1 (condition should have fired)", running)
	}

	// Clean up
	sched.mu.Lock()
	for _, entry := range sched.running {
		entry.cancel()
	}
	sched.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
}

func TestScheduler_GitTrigger(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	_ = os.MkdirAll(repoDir, 0755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	// Init a git repo with an initial commit
	for _, cmd := range []string{
		"git -C " + repoDir + " init",
		"git -C " + repoDir + " config user.email test@test.com",
		"git -C " + repoDir + " config user.name test",
	} {
		if out, err := exec.Command("sh", "-c", cmd).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", cmd, err, out)
		}
	}
	_ = os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("v1"), 0644)
	for _, cmd := range []string{
		"git -C " + repoDir + " add .",
		"git -C " + repoDir + " commit -m initial",
	} {
		if out, err := exec.Command("sh", "-c", cmd).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", cmd, err, out)
		}
	}

	// Place the DAG in the repo dir so the git trigger uses it as SourceDir
	writeDAG(t, repoDir, "git-watch.yaml", `
name: git-watch
trigger:
  git:
    branch: HEAD
    poll_interval: 200ms
steps:
  - id: deploy
    command: sleep 10
`)

	sched := New([]DAGSource{{Name: "test", Dir: repoDir}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = sched.syncDAGs(ctx)

	// First poll learns the current hash — no trigger
	time.Sleep(500 * time.Millisecond)
	sched.mu.Lock()
	running := len(sched.running)
	sched.mu.Unlock()
	if running != 0 {
		t.Fatalf("running = %d, want 0 (no new commits yet)", running)
	}

	// Make a new commit
	_ = os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("v2"), 0644)
	for _, cmd := range []string{
		"git -C " + repoDir + " add .",
		"git -C " + repoDir + " commit -m second",
	} {
		if out, err := exec.Command("sh", "-c", cmd).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", cmd, err, out)
		}
	}

	// Wait for poll to detect the new commit
	time.Sleep(500 * time.Millisecond)

	sched.mu.Lock()
	running = len(sched.running)
	sched.mu.Unlock()
	if running != 1 {
		t.Errorf("running = %d, want 1 (git trigger should have fired)", running)
	}

	// Clean up
	sched.mu.Lock()
	for _, entry := range sched.running {
		entry.cancel()
	}
	sched.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
}

func TestScheduler_WebhookTrigger(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	writeDAG(t, dagDir, "webhook.yaml", `
name: webhook-dag
trigger:
  webhook: {}
steps:
  - id: deploy
    command: sleep 10
`)

	sched := New([]DAGSource{{Name: "test", Dir: dagDir}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = sched.syncDAGs(ctx)

	// Webhook server should have started
	sched.mu.Lock()
	hasServer := sched.webhookCloseFn != nil
	sched.mu.Unlock()
	if !hasServer {
		t.Fatal("expected webhook server to be started")
	}

	// Send POST to trigger the DAG
	sched.mu.Lock()
	addr := sched.webhookAddr
	sched.mu.Unlock()

	resp, err := http.Post(
		fmt.Sprintf("http://%s/webhook/webhook-dag", addr),
		"application/json",
		strings.NewReader("{}"),
	)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}

	time.Sleep(200 * time.Millisecond)

	sched.mu.Lock()
	running := len(sched.running)
	sched.mu.Unlock()
	if running != 1 {
		t.Errorf("running = %d, want 1", running)
	}

	// Clean up
	sched.mu.Lock()
	for _, entry := range sched.running {
		entry.cancel()
	}
	sched.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
}

func TestScheduler_WebhookHMACValidation(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	writeDAG(t, dagDir, "secure.yaml", `
name: secure-dag
trigger:
  webhook:
    secret: my-secret-key
steps:
  - id: deploy
    command: sleep 10
`)

	sched := New([]DAGSource{{Name: "test", Dir: dagDir}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = sched.syncDAGs(ctx)

	sched.mu.Lock()
	addr := sched.webhookAddr
	sched.mu.Unlock()
	baseURL := fmt.Sprintf("http://%s/webhook/secure-dag", addr)

	// Request without signature → 403
	resp, err := http.Post(baseURL, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("no sig: status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}

	// Request with wrong signature → 403
	req, _ := http.NewRequest("POST", baseURL, strings.NewReader("{}"))
	req.Header.Set("X-Daggle-Signature", "sha256=0000000000000000000000000000000000000000000000000000000000000000")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("wrong sig: status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}

	// Request with correct signature → 202
	body := []byte("{}")
	mac := hmac.New(sha256.New, []byte("my-secret-key"))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, _ = http.NewRequest("POST", baseURL, strings.NewReader("{}"))
	req.Header.Set("X-Daggle-Signature", sig)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("correct sig: status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}

	// Clean up
	time.Sleep(200 * time.Millisecond)
	sched.mu.Lock()
	for _, entry := range sched.running {
		entry.cancel()
	}
	sched.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
}

func writeDAG(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("write DAG %s: %v", name, err)
	}
}
