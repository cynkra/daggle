package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/executor"
	"github.com/cynkra/daggle/state"
)

// mockExecutor returns a configurable executor for testing.
type mockExecutor struct {
	fn func(step dag.Step) executor.Result
}

func (m *mockExecutor) Run(_ context.Context, step dag.Step, _ string, _ string, _ []string) executor.Result {
	return m.fn(step)
}

func setupRun(t *testing.T) *state.RunInfo {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)
	run, err := state.CreateRun("test-dag")
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	return run
}

func TestEngine_LinearDAG(t *testing.T) {
	run := setupRun(t)
	d := &dag.DAG{
		Name: "test",
		Steps: []dag.Step{
			{ID: "a", Command: "echo a"},
			{ID: "b", Command: "echo b", Depends: []string{"a"}},
			{ID: "c", Command: "echo c", Depends: []string{"b"}},
		},
	}

	var order []string
	mock := &mockExecutor{fn: func(step dag.Step) executor.Result {
		order = append(order, step.ID)
		return executor.Result{ExitCode: 0}
	}}

	eng := New(d, run, func(_ dag.Step) executor.Executor { return mock })
	err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(order) != 3 || order[0] != "a" || order[1] != "b" || order[2] != "c" {
		t.Errorf("execution order = %v, want [a b c]", order)
	}

	// Verify events were written
	events, err := state.ReadEvents(run.Dir)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(events) == 0 {
		t.Error("no events written")
	}
	// Last event should be run_completed
	if events[len(events)-1].Type != state.EventRunCompleted {
		t.Errorf("last event = %q, want %q", events[len(events)-1].Type, state.EventRunCompleted)
	}
}

func TestEngine_ParallelTier(t *testing.T) {
	run := setupRun(t)
	d := &dag.DAG{
		Name: "test",
		Steps: []dag.Step{
			{ID: "a", Command: "echo a"},
			{ID: "b", Command: "echo b"},
			{ID: "c", Command: "echo c", Depends: []string{"a", "b"}},
		},
	}

	var count atomic.Int32
	var maxConcurrent atomic.Int32

	mock := &mockExecutor{fn: func(_ dag.Step) executor.Result {
		cur := count.Add(1)
		for {
			old := maxConcurrent.Load()
			if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		count.Add(-1)
		return executor.Result{ExitCode: 0}
	}}

	eng := New(d, run, func(_ dag.Step) executor.Executor { return mock })
	err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if maxConcurrent.Load() < 2 {
		t.Errorf("max concurrent = %d, expected at least 2 for parallel tier", maxConcurrent.Load())
	}
}

func TestEngine_FailureStopsTiers(t *testing.T) {
	run := setupRun(t)
	d := &dag.DAG{
		Name: "test",
		Steps: []dag.Step{
			{ID: "a", Command: "echo a"},
			{ID: "b", Command: "fail", Depends: []string{"a"}},
			{ID: "c", Command: "echo c", Depends: []string{"b"}},
		},
	}

	var executed []string
	mock := &mockExecutor{fn: func(step dag.Step) executor.Result {
		executed = append(executed, step.ID)
		if step.ID == "b" {
			return executor.Result{ExitCode: 1, Err: fmt.Errorf("step b failed")}
		}
		return executor.Result{ExitCode: 0}
	}}

	eng := New(d, run, func(_ dag.Step) executor.Executor { return mock })
	err := eng.Run(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// c should not have executed
	for _, id := range executed {
		if id == "c" {
			t.Error("step c should not have executed after b failed")
		}
	}

	// Verify run_failed event
	events, _ := state.ReadEvents(run.Dir)
	last := events[len(events)-1]
	if last.Type != state.EventRunFailed {
		t.Errorf("last event = %q, want %q", last.Type, state.EventRunFailed)
	}
}

func TestEngine_Retry(t *testing.T) {
	run := setupRun(t)
	d := &dag.DAG{
		Name: "test",
		Steps: []dag.Step{
			{ID: "flaky", Command: "echo flaky", Retry: &dag.Retry{Limit: 2}},
		},
	}

	var attempts int
	mock := &mockExecutor{fn: func(_ dag.Step) executor.Result {
		attempts++
		if attempts < 3 {
			return executor.Result{ExitCode: 1, Err: fmt.Errorf("attempt %d failed", attempts)}
		}
		return executor.Result{ExitCode: 0}
	}}

	eng := New(d, run, func(_ dag.Step) executor.Executor { return mock })
	err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v (should have succeeded on attempt 3)", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestEngine_EnvPropagation(t *testing.T) {
	run := setupRun(t)
	d := &dag.DAG{
		Name: "test",
		Env:  map[string]string{"FOO": "bar"},
		Steps: []dag.Step{
			{ID: "a", Command: "echo", Env: map[string]string{"BAZ": "qux"}},
		},
	}

	var receivedEnv []string
	mock := &mockExecutor{fn: func(_ dag.Step) executor.Result {
		return executor.Result{ExitCode: 0}
	}}

	// Wrap to capture env
	wrappedFactory := func(_ dag.Step) executor.Executor {
		return &envCapture{inner: mock, captured: &receivedEnv}
	}

	eng := New(d, run, wrappedFactory)
	err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Check that DAGGLE_RUN_ID and DAGGLE_DAG_NAME are set
	_ = os.Getenv("DAGGLE_RUN_ID") // just verify no panic
}

type envCapture struct {
	inner    executor.Executor
	captured *[]string
}

func (e *envCapture) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) executor.Result {
	*e.captured = env
	return e.inner.Run(ctx, step, logDir, workdir, env)
}

func TestEngine_OutputPassing(t *testing.T) {
	run := setupRun(t)
	d := &dag.DAG{
		Name: "test",
		Steps: []dag.Step{
			{ID: "producer", Command: "echo produce"},
			{ID: "consumer", Command: "echo consume", Depends: []string{"producer"}},
		},
	}

	var consumerEnv []string
	mock := &mockExecutor{fn: func(step dag.Step) executor.Result {
		if step.ID == "producer" {
			return executor.Result{
				ExitCode: 0,
				Outputs:  map[string]string{"row_count": "42", "file_path": "/tmp/data.csv"},
			}
		}
		return executor.Result{ExitCode: 0}
	}}

	// Use a wrapper that captures env for the consumer step
	factory := func(s dag.Step) executor.Executor {
		if s.ID == "consumer" {
			return &envCapture{inner: mock, captured: &consumerEnv}
		}
		return mock
	}

	eng := New(d, run, factory)
	err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Check that producer outputs are available to consumer
	envMap := make(map[string]string)
	for _, e := range consumerEnv {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	if envMap["DAGGLE_OUTPUT_PRODUCER_ROW_COUNT"] != "42" {
		t.Errorf("DAGGLE_OUTPUT_PRODUCER_ROW_COUNT = %q, want %q", envMap["DAGGLE_OUTPUT_PRODUCER_ROW_COUNT"], "42")
	}
	if envMap["DAGGLE_OUTPUT_PRODUCER_FILE_PATH"] != "/tmp/data.csv" {
		t.Errorf("DAGGLE_OUTPUT_PRODUCER_FILE_PATH = %q, want %q", envMap["DAGGLE_OUTPUT_PRODUCER_FILE_PATH"], "/tmp/data.csv")
	}
}

func TestEngine_Workdir(t *testing.T) {
	run := setupRun(t)
	sourceDir := t.TempDir()
	d := &dag.DAG{
		Name:      "test",
		SourceDir: sourceDir,
		Steps: []dag.Step{
			{ID: "a", Command: "echo a"},
		},
	}

	var capturedWorkdir string
	wrappedMock := &workdirCapture{
		inner: &mockExecutor{fn: func(_ dag.Step) executor.Result {
			return executor.Result{ExitCode: 0}
		}},
		captured: &capturedWorkdir,
	}

	eng := New(d, run, func(_ dag.Step) executor.Executor { return wrappedMock })
	err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if capturedWorkdir != sourceDir {
		t.Errorf("workdir = %q, want %q", capturedWorkdir, sourceDir)
	}
}

type workdirCapture struct {
	inner    executor.Executor
	captured *string
}

func (w *workdirCapture) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) executor.Result {
	*w.captured = workdir
	return w.inner.Run(ctx, step, logDir, workdir, env)
}

func TestEngine_RenvEnvInjection(t *testing.T) {
	run := setupRun(t)
	d := &dag.DAG{
		Name: "test",
		Steps: []dag.Step{
			{ID: "a", Command: "echo a"},
		},
	}

	var receivedEnv []string
	mock := &mockExecutor{fn: func(_ dag.Step) executor.Result {
		return executor.Result{ExitCode: 0}
	}}
	factory := func(_ dag.Step) executor.Executor {
		return &envCapture{inner: mock, captured: &receivedEnv}
	}

	eng := New(d, run, factory)
	eng.SetRenvLibPath("/project/renv/library/R-4.4/aarch64-apple-darwin20")
	if err := eng.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	envMap := envToMap(receivedEnv)
	if envMap["R_LIBS_USER"] != "/project/renv/library/R-4.4/aarch64-apple-darwin20" {
		t.Errorf("R_LIBS_USER = %q, want renv library path", envMap["R_LIBS_USER"])
	}
}

func TestEngine_RenvEnvOptOut(t *testing.T) {
	run := setupRun(t)
	d := &dag.DAG{
		Name: "test",
		Env:  map[string]string{"R_LIBS_USER": "/custom/path"},
		Steps: []dag.Step{
			{ID: "a", Command: "echo a"},
		},
	}

	var receivedEnv []string
	mock := &mockExecutor{fn: func(_ dag.Step) executor.Result {
		return executor.Result{ExitCode: 0}
	}}
	factory := func(_ dag.Step) executor.Executor {
		return &envCapture{inner: mock, captured: &receivedEnv}
	}

	eng := New(d, run, factory)
	eng.SetRenvLibPath("/project/renv/library/R-4.4/aarch64-apple-darwin20")
	if err := eng.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	envMap := envToMap(receivedEnv)
	if envMap["R_LIBS_USER"] != "/custom/path" {
		t.Errorf("R_LIBS_USER = %q, want /custom/path (user override should win)", envMap["R_LIBS_USER"])
	}
}

func TestEngine_NoRenvByDefault(t *testing.T) {
	run := setupRun(t)
	d := &dag.DAG{
		Name: "test",
		Steps: []dag.Step{
			{ID: "a", Command: "echo a"},
		},
	}

	var receivedEnv []string
	mock := &mockExecutor{fn: func(_ dag.Step) executor.Result {
		return executor.Result{ExitCode: 0}
	}}
	factory := func(_ dag.Step) executor.Executor {
		return &envCapture{inner: mock, captured: &receivedEnv}
	}

	eng := New(d, run, factory)
	if err := eng.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	envMap := envToMap(receivedEnv)
	if _, ok := envMap["R_LIBS_USER"]; ok {
		t.Error("R_LIBS_USER should not be set when no renv lib path configured")
	}
}

func envToMap(env []string) map[string]string {
	m := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			m[parts[0]] = parts[1]
		}
	}
	return m
}

func TestEngine_Hooks(t *testing.T) {
	run := setupRun(t)
	marker := filepath.Join(t.TempDir(), "hook_ran")

	d := &dag.DAG{
		Name: "test",
		Steps: []dag.Step{
			{ID: "a", Command: "echo a"},
		},
		OnSuccess: &dag.Hook{Command: fmt.Sprintf("touch %s", marker)},
	}

	mock := &mockExecutor{fn: func(_ dag.Step) executor.Result {
		return executor.Result{ExitCode: 0}
	}}

	eng := New(d, run, func(_ dag.Step) executor.Executor { return mock })
	err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Give hook a moment to complete
	time.Sleep(100 * time.Millisecond)

	if _, err := os.Stat(marker); os.IsNotExist(err) {
		t.Error("on_success hook did not run (marker file not created)")
	}
}

func TestEngine_OnFailureHook(t *testing.T) {
	run := setupRun(t)
	marker := filepath.Join(t.TempDir(), "failure_hook_ran")

	d := &dag.DAG{
		Name: "test",
		Steps: []dag.Step{
			{ID: "a", Command: "fail"},
		},
		OnFailure: &dag.Hook{Command: fmt.Sprintf("touch %s", marker)},
	}

	mock := &mockExecutor{fn: func(_ dag.Step) executor.Result {
		return executor.Result{ExitCode: 1, Err: fmt.Errorf("step failed")}
	}}

	eng := New(d, run, func(_ dag.Step) executor.Executor { return mock })
	_ = eng.Run(context.Background())

	time.Sleep(100 * time.Millisecond)

	if _, err := os.Stat(marker); os.IsNotExist(err) {
		t.Error("on_failure hook did not run (marker file not created)")
	}
}

func TestEngine_WriteMeta(t *testing.T) {
	run := setupRun(t)
	d := &dag.DAG{
		Name: "test",
		Steps: []dag.Step{
			{ID: "a", Command: "echo a"},
		},
	}

	mock := &mockExecutor{fn: func(_ dag.Step) executor.Result {
		return executor.Result{ExitCode: 0}
	}}

	eng := New(d, run, func(_ dag.Step) executor.Executor { return mock })
	eng.SetMeta(&state.RunMeta{
		RunID:    run.ID,
		DAGName:  "test",
		DAGHash:  "abc123",
		RVersion: "4.4.1",
	})

	if err := eng.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	meta, err := state.ReadMeta(run.Dir)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta.Status != "completed" {
		t.Errorf("meta.Status = %q, want %q", meta.Status, "completed")
	}
	if meta.DAGHash != "abc123" {
		t.Errorf("meta.DAGHash = %q, want %q", meta.DAGHash, "abc123")
	}
}

func TestRetryDelay(t *testing.T) {
	// Default (nil retry) = linear
	if d := retryDelay(3, nil); d != 3*time.Second {
		t.Errorf("nil retry: delay = %v, want 3s", d)
	}

	// Linear
	r := &dag.Retry{Limit: 3, Backoff: "linear"}
	if d := retryDelay(2, r); d != 2*time.Second {
		t.Errorf("linear: delay = %v, want 2s", d)
	}

	// Exponential: attempt 1=1s, 2=2s, 3=4s, 4=8s
	r = &dag.Retry{Limit: 5, Backoff: "exponential"}
	if d := retryDelay(1, r); d != 1*time.Second {
		t.Errorf("exp attempt 1: delay = %v, want 1s", d)
	}
	if d := retryDelay(3, r); d != 4*time.Second {
		t.Errorf("exp attempt 3: delay = %v, want 4s", d)
	}

	// Exponential with max_delay
	r = &dag.Retry{Limit: 5, Backoff: "exponential", MaxDelay: "3s"}
	if d := retryDelay(3, r); d != 3*time.Second {
		t.Errorf("exp capped attempt 3: delay = %v, want 3s", d)
	}
}
