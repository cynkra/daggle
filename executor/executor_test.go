package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schochastics/rdag/dag"
)

func TestShellExecutor_Success(t *testing.T) {
	logDir := t.TempDir()
	step := dag.Step{ID: "test-echo", Command: "echo hello"}

	exec := &ShellExecutor{}
	result := exec.Run(context.Background(), step, logDir, nil)

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}

	// Check stdout log was created
	stdout, err := os.ReadFile(filepath.Join(logDir, "test-echo.stdout.log"))
	if err != nil {
		t.Fatalf("read stdout log: %v", err)
	}
	if string(stdout) != "hello\n" {
		t.Errorf("stdout = %q, want %q", string(stdout), "hello\n")
	}
}

func TestShellExecutor_Failure(t *testing.T) {
	logDir := t.TempDir()
	step := dag.Step{ID: "test-fail", Command: "exit 42"}

	exec := &ShellExecutor{}
	result := exec.Run(context.Background(), step, logDir, nil)

	if result.Err == nil {
		t.Fatal("expected error, got nil")
	}
	if result.ExitCode != 42 {
		t.Errorf("exit code = %d, want 42", result.ExitCode)
	}
}

func TestShellExecutor_Timeout(t *testing.T) {
	logDir := t.TempDir()
	step := dag.Step{ID: "test-timeout", Command: "sleep 60"}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	exec := &ShellExecutor{}
	result := exec.Run(ctx, step, logDir, nil)

	if result.Err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if result.ExitCode != -1 {
		t.Errorf("exit code = %d, want -1", result.ExitCode)
	}
}

func TestShellExecutor_EnvVars(t *testing.T) {
	logDir := t.TempDir()
	step := dag.Step{ID: "test-env", Command: "echo $RDAG_TEST_VAR"}

	exec := &ShellExecutor{}
	result := exec.Run(context.Background(), step, logDir, []string{"RDAG_TEST_VAR=hello_rdag"})

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}

	stdout, _ := os.ReadFile(filepath.Join(logDir, "test-env.stdout.log"))
	if string(stdout) != "hello_rdag\n" {
		t.Errorf("stdout = %q, want %q", string(stdout), "hello_rdag\n")
	}
}

func TestInlineRExecutor_WritesFile(t *testing.T) {
	logDir := t.TempDir()
	step := dag.Step{ID: "test-inline", RExpr: "cat('hello from R\\n')"}

	exec := &InlineRExecutor{}
	// Just test that the .R file gets created (don't require Rscript)
	_ = exec.Run(context.Background(), step, logDir, nil)

	rFile := filepath.Join(logDir, "test-inline.inline.R")
	content, err := os.ReadFile(rFile)
	if err != nil {
		t.Fatalf("inline R file not created: %v", err)
	}
	if string(content) != "cat('hello from R\\n')" {
		t.Errorf("inline R content = %q", string(content))
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		step dag.Step
		want string
	}{
		{dag.Step{Script: "foo.R"}, "*executor.ScriptExecutor"},
		{dag.Step{RExpr: "1+1"}, "*executor.InlineRExecutor"},
		{dag.Step{Command: "echo"}, "*executor.ShellExecutor"},
	}

	for _, tt := range tests {
		e := New(tt.step)
		if e == nil {
			t.Errorf("New(%+v) = nil", tt.step)
			continue
		}
		got := typeString(e)
		if got != tt.want {
			t.Errorf("New(%+v) type = %s, want %s", tt.step, got, tt.want)
		}
	}
}

func typeString(e Executor) string {
	return fmt.Sprintf("%T", e)
}

