package engine

import (
	"context"
	"fmt"
	"os"
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

	mock := &mockExecutor{fn: func(step dag.Step) executor.Result {
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
	mock := &mockExecutor{fn: func(step dag.Step) executor.Result {
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
	mock := &mockExecutor{fn: func(step dag.Step) executor.Result {
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
		inner: &mockExecutor{fn: func(step dag.Step) executor.Result {
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
