package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cynkra/daggle/dag"
)

func TestShellExecutor_Success(t *testing.T) {
	logDir := t.TempDir()
	step := dag.Step{ID: "test-echo", Command: "echo hello"}

	exec := &ShellExecutor{}
	result := exec.Run(context.Background(), step, logDir, "", nil)

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}

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
	result := exec.Run(context.Background(), step, logDir, "", nil)

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
	result := exec.Run(ctx, step, logDir, "", nil)

	if result.Err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if result.ExitCode != -1 {
		t.Errorf("exit code = %d, want -1", result.ExitCode)
	}
}

func TestShellExecutor_EnvVars(t *testing.T) {
	logDir := t.TempDir()
	step := dag.Step{ID: "test-env", Command: "echo $DAGGLE_TEST_VAR"}

	exec := &ShellExecutor{}
	result := exec.Run(context.Background(), step, logDir, "", []string{"DAGGLE_TEST_VAR=hello_daggle"})

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}

	stdout, _ := os.ReadFile(filepath.Join(logDir, "test-env.stdout.log"))
	if string(stdout) != "hello_daggle\n" {
		t.Errorf("stdout = %q, want %q", string(stdout), "hello_daggle\n")
	}
}

func TestShellExecutor_Workdir(t *testing.T) {
	logDir := t.TempDir()
	workdir := t.TempDir()
	step := dag.Step{ID: "test-workdir", Command: "pwd"}

	exec := &ShellExecutor{}
	result := exec.Run(context.Background(), step, logDir, workdir, nil)

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}

	stdout, _ := os.ReadFile(filepath.Join(logDir, "test-workdir.stdout.log"))
	// Resolve symlinks for macOS (/var -> /private/var)
	got, _ := filepath.EvalSymlinks(filepath.Clean(string(stdout[:len(stdout)-1])))
	want, _ := filepath.EvalSymlinks(filepath.Clean(workdir))
	if got != want {
		t.Errorf("workdir = %q, want %q", got, want)
	}
}

func TestShellExecutor_OutputMarkers(t *testing.T) {
	logDir := t.TempDir()
	step := dag.Step{
		ID:      "test-output",
		Command: `echo "::daggle-output name=row_count::42" && echo "normal line" && echo "::daggle-output name=file_path::/tmp/data.csv"`,
	}

	exec := &ShellExecutor{}
	result := exec.Run(context.Background(), step, logDir, "", nil)

	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}

	if len(result.Outputs) != 2 {
		t.Fatalf("outputs = %d, want 2: %v", len(result.Outputs), result.Outputs)
	}
	if result.Outputs["row_count"] != "42" {
		t.Errorf("row_count = %q, want %q", result.Outputs["row_count"], "42")
	}
	if result.Outputs["file_path"] != "/tmp/data.csv" {
		t.Errorf("file_path = %q, want %q", result.Outputs["file_path"], "/tmp/data.csv")
	}
}

func TestNew_AllStepTypes(t *testing.T) {
	tests := []struct {
		step dag.Step
		want string
	}{
		{dag.Step{Script: "foo.R"}, "*executor.ScriptExecutor"},
		{dag.Step{RExpr: "1+1"}, "*executor.InlineRExecutor"},
		{dag.Step{Command: "echo"}, "*executor.ShellExecutor"},
		{dag.Step{Quarto: "report.qmd"}, "*executor.QuartoExecutor"},
		{dag.Step{Test: "."}, "*executor.RPkgExecutor"},
		{dag.Step{Check: "."}, "*executor.RPkgExecutor"},
		{dag.Step{Document: "."}, "*executor.RPkgExecutor"},
		{dag.Step{Lint: "."}, "*executor.RPkgExecutor"},
		{dag.Step{Style: "."}, "*executor.RPkgExecutor"},
		{dag.Step{Connect: &dag.ConnectDeploy{Type: "shiny", Path: "app/"}}, "*executor.ConnectExecutor"},
		{dag.Step{}, "<nil>"},
	}
	for _, tt := range tests {
		ex := New(tt.step)
		got := fmt.Sprintf("%T", ex)
		if got != tt.want {
			t.Errorf("New(%v) = %s, want %s", dag.StepType(tt.step), got, tt.want)
		}
	}
}

func TestExtractRError(t *testing.T) {
	tmpDir := t.TempDir()

	// Test with R error pattern
	stderrPath := filepath.Join(tmpDir, "test.stderr.log")
	_ = os.WriteFile(stderrPath, []byte("some output\nWarning message:\nError in foo(): bar is not defined\nExecution halted\n"), 0644)

	detail := extractRError(stderrPath)
	if detail == "" {
		t.Fatal("expected error detail, got empty string")
	}
	if !strings.Contains(detail, "Error in foo()") {
		t.Errorf("error detail = %q, want it to contain 'Error in foo()'", detail)
	}

	// Test with no R error
	noErrPath := filepath.Join(tmpDir, "noerr.stderr.log")
	_ = os.WriteFile(noErrPath, []byte("just some warnings\n"), 0644)

	detail = extractRError(noErrPath)
	if detail != "" {
		t.Errorf("expected empty detail for non-error, got %q", detail)
	}

	// Test with nonexistent file
	detail = extractRError(filepath.Join(tmpDir, "nonexistent"))
	if detail != "" {
		t.Errorf("expected empty detail for missing file, got %q", detail)
	}
}

func TestInlineRExecutor_WritesFile(t *testing.T) {
	logDir := t.TempDir()
	step := dag.Step{ID: "test-inline", RExpr: "cat('hello from R\\n')"}

	exec := &InlineRExecutor{}
	_ = exec.Run(context.Background(), step, logDir, "", nil)

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

func TestRPkgExecutor_GenerateRCode(t *testing.T) {
	tests := []struct {
		action  string
		want    []string // substrings that must appear in generated code
		notWant []string // substrings that must not appear
	}{
		{"test", []string{"devtools::test", "requireNamespace", "devtools", "daggle-output name=test_failures"}, nil},
		{"check", []string{"rcmdcheck::rcmdcheck", "requireNamespace", "rcmdcheck", "daggle-output name=check_errors"}, nil},
		{"document", []string{"roxygen2::roxygenize", "requireNamespace", "roxygen2"}, nil},
		{"lint", []string{"lintr::lint_package", "requireNamespace", "lintr", "daggle-output name=lint_issues"}, nil},
		{"style", []string{"styler::style_pkg", "requireNamespace", "styler", "daggle-output name=files_changed"}, nil},
		{"coverage", []string{"covr::package_coverage", "requireNamespace", "covr", "daggle-output name=coverage_pct"}, nil},
		{"renv_restore", []string{"renv::restore", "requireNamespace", "renv", "prompt = FALSE"}, nil},
		{"unknown_action", []string{"stop", "unknown rpkg action"}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			e := &RPkgExecutor{Action: tt.action}
			code := e.generateRCode(".")
			for _, want := range tt.want {
				if !strings.Contains(code, want) {
					t.Errorf("generateRCode(%q) missing %q in:\n%s", tt.action, want, code)
				}
			}
			for _, notWant := range tt.notWant {
				if strings.Contains(code, notWant) {
					t.Errorf("generateRCode(%q) should not contain %q", tt.action, notWant)
				}
			}
		})
	}
}

func TestRPkgExecutor_ResolvePkgPath(t *testing.T) {
	tests := []struct {
		action string
		step   dag.Step
		want   string
	}{
		{"test", dag.Step{Test: "."}, "."},
		{"test", dag.Step{Test: "true"}, "."},
		{"test", dag.Step{Test: "/custom/path"}, "/custom/path"},
		{"lint", dag.Step{Lint: "."}, "."},
		{"lint", dag.Step{Lint: "pkg/"}, "pkg/"},
		{"coverage", dag.Step{Coverage: ""}, "."},
	}
	for _, tt := range tests {
		t.Run(tt.action+"_"+tt.want, func(t *testing.T) {
			e := &RPkgExecutor{Action: tt.action}
			if got := e.resolvePkgPath(tt.step); got != tt.want {
				t.Errorf("resolvePkgPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenerateConnectR(t *testing.T) {
	tests := []struct {
		name string
		c    *dag.ConnectDeploy
		want []string
	}{
		{
			"shiny",
			&dag.ConnectDeploy{Type: "shiny", Path: "app/", Name: "my-app"},
			[]string{"rsconnect::deployApp", "my-app", "CONNECT_SERVER", "CONNECT_API_KEY", "daggle-output name=connect_url"},
		},
		{
			"quarto",
			&dag.ConnectDeploy{Type: "quarto", Path: "report.qmd"},
			[]string{"rsconnect::deployDoc", "report.qmd", "daggle-output name=connect_app"},
		},
		{
			"plumber",
			&dag.ConnectDeploy{Type: "plumber", Path: "api/", Name: "my-api"},
			[]string{"rsconnect::deployAPI", "my-api"},
		},
		{
			"default name from path",
			&dag.ConnectDeploy{Type: "shiny", Path: "apps/dashboard/"},
			[]string{"dashboard"}, // default name = last path component
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := generateConnectR(tt.c)
			for _, want := range tt.want {
				if !strings.Contains(code, want) {
					t.Errorf("generateConnectR(%s) missing %q in:\n%s", tt.name, want, code)
				}
			}
		})
	}

	// Test force_update = false
	f := false
	code := generateConnectR(&dag.ConnectDeploy{Type: "shiny", Path: "app/", ForceUpdate: &f})
	if !strings.Contains(code, "FALSE") {
		t.Error("force_update=false should generate FALSE")
	}
}
