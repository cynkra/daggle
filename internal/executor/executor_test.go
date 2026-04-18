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

func TestExtractErrorDetail_RError(t *testing.T) {
	tmpDir := t.TempDir()

	// Test with R error pattern
	stderrPath := filepath.Join(tmpDir, "test.stderr.log")
	_ = os.WriteFile(stderrPath, []byte("some output\nWarning message:\nError in foo(): bar is not defined\nExecution halted\n"), 0o644)

	detail := extractErrorDetail(stderrPath)
	if detail == "" {
		t.Fatal("expected error detail, got empty string")
	}
	if !strings.Contains(detail, "Error in foo()") {
		t.Errorf("error detail = %q, want it to contain 'Error in foo()'", detail)
	}
	if strings.Contains(detail, "Execution halted") {
		t.Errorf("error detail should not contain 'Execution halted'")
	}
}

func TestExtractErrorDetail_QuartoError(t *testing.T) {
	tmpDir := t.TempDir()

	stderrPath := filepath.Join(tmpDir, "quarto.stderr.log")
	_ = os.WriteFile(stderrPath, []byte("Rendering document.qmd\nERROR: YAMLError: bad indentation of a mapping entry at line 15\n"), 0o644)

	detail := extractErrorDetail(stderrPath)
	if !strings.Contains(detail, "ERROR: YAMLError") {
		t.Errorf("error detail = %q, want it to contain 'ERROR: YAMLError'", detail)
	}
}

func TestExtractLastLines_OrderAndFiltering(t *testing.T) {
	lines := []string{"one", "", "two", "  ", "three", "four", "five"}
	got := extractLastLines(lines, 3)
	want := "three\nfour\nfive"
	if got != want {
		t.Errorf("extractLastLines(n=3) = %q, want %q", got, want)
	}

	// All non-empty, n larger than input
	got = extractLastLines([]string{"a", "b"}, 10)
	if got != "a\nb" {
		t.Errorf("extractLastLines(n=10) = %q, want 'a\\nb'", got)
	}

	// n=0 returns empty
	if got := extractLastLines(lines, 0); got != "" {
		t.Errorf("n=0 should return empty, got %q", got)
	}

	// Only empty/whitespace lines -> empty result
	if got := extractLastLines([]string{"", "   ", "\t"}, 5); got != "" {
		t.Errorf("whitespace-only should return empty, got %q", got)
	}
}

func TestExtractErrorDetail_ShellFallback(t *testing.T) {
	tmpDir := t.TempDir()

	stderrPath := filepath.Join(tmpDir, "shell.stderr.log")
	_ = os.WriteFile(stderrPath, []byte("ls: cannot access '/no/such/path': No such file or directory\n"), 0o644)

	detail := extractErrorDetail(stderrPath)
	if !strings.Contains(detail, "No such file or directory") {
		t.Errorf("error detail = %q, want it to contain 'No such file or directory'", detail)
	}
}

func TestExtractErrorDetail_EmptyStderr(t *testing.T) {
	tmpDir := t.TempDir()

	// Empty file
	emptyPath := filepath.Join(tmpDir, "empty.stderr.log")
	_ = os.WriteFile(emptyPath, []byte(""), 0o644)
	if detail := extractErrorDetail(emptyPath); detail != "" {
		t.Errorf("expected empty detail for empty stderr, got %q", detail)
	}

	// Nonexistent file
	if detail := extractErrorDetail(filepath.Join(tmpDir, "nonexistent")); detail != "" {
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
	// Content is wrapped with sessionInfo-on-failure handler; the user code
	// must still appear somewhere in the file.
	if !strings.Contains(string(content), "cat('hello from R\\n')") {
		t.Errorf("inline R content missing user code: %q", string(content))
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

func TestRPkgExecutor_NewStepTypes(t *testing.T) {
	tests := []struct {
		action string
		want   []string
	}{
		{"shinytest", []string{"shinytest2::test_app", "requireNamespace", "shinytest2"}},
		{"pkgdown", []string{"pkgdown::build_site", "requireNamespace", "pkgdown"}},
		{"install", []string{"pak::pkg_install", "install.packages"}},
		{"targets", []string{"targets::tar_make", "requireNamespace", "targets"}},
		{"benchmark", []string{"bench", "requireNamespace", "source"}},
		{"revdepcheck", []string{"revdepcheck::revdep_check", "requireNamespace"}},
	}
	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			e := &RPkgExecutor{Action: tt.action}
			code := e.generateRCode(".")
			for _, want := range tt.want {
				if !strings.Contains(code, want) {
					t.Errorf("generateRCode(%q) missing %q", tt.action, want)
				}
			}
		})
	}
}

func TestApproveExecutor_Approved(t *testing.T) {
	logDir := t.TempDir()
	step := dag.Step{
		ID:      "review",
		Approve: &dag.ApproveStep{Message: "Please review"},
	}

	// Write approval event after a short delay
	go func() {
		time.Sleep(500 * time.Millisecond)
		_ = WriteApprovalEvent(logDir, "review", true)
	}()

	exec := &ApproveExecutor{}
	result := exec.Run(context.Background(), step, logDir, "", nil)

	if result.Err != nil {
		t.Fatalf("expected approval success, got: %v", result.Err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}
}

func TestApproveExecutor_Rejected(t *testing.T) {
	logDir := t.TempDir()
	step := dag.Step{
		ID:      "review",
		Approve: &dag.ApproveStep{Message: "Please review"},
	}

	// Write rejection event after a short delay
	go func() {
		time.Sleep(500 * time.Millisecond)
		_ = WriteApprovalEvent(logDir, "review", false)
	}()

	exec := &ApproveExecutor{}
	result := exec.Run(context.Background(), step, logDir, "", nil)

	if result.Err == nil {
		t.Fatal("expected rejection error, got nil")
	}
	if result.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", result.ExitCode)
	}
}

func TestApproveExecutor_Timeout(t *testing.T) {
	logDir := t.TempDir()
	step := dag.Step{
		ID:      "review",
		Approve: &dag.ApproveStep{Message: "Review", Timeout: "1s"},
	}

	exec := &ApproveExecutor{}
	result := exec.Run(context.Background(), step, logDir, "", nil)

	if result.Err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(result.Err.Error(), "timed out") {
		t.Errorf("error = %q, want timeout message", result.Err.Error())
	}
}

func TestNew_Phase4StepTypes(t *testing.T) {
	tests := []struct {
		step dag.Step
		want string
	}{
		{dag.Step{Shinytest: "app/"}, "*executor.RPkgExecutor"},
		{dag.Step{Pkgdown: "."}, "*executor.RPkgExecutor"},
		{dag.Step{Install: "dplyr"}, "*executor.RPkgExecutor"},
		{dag.Step{Targets: "."}, "*executor.RPkgExecutor"},
		{dag.Step{Benchmark: "bench/"}, "*executor.RPkgExecutor"},
		{dag.Step{Revdepcheck: "."}, "*executor.RPkgExecutor"},
		{dag.Step{Pin: &dag.PinDeploy{Board: "local", Name: "data", Object: "data.rds"}}, "*executor.PinExecutor"},
		{dag.Step{Vetiver: &dag.VetiverDeploy{Action: "pin", Name: "model", Board: "local", Model: "model.rds"}}, "*executor.VetiverExecutor"},
	}
	for _, tt := range tests {
		ex := New(tt.step)
		got := fmt.Sprintf("%T", ex)
		if got != tt.want {
			t.Errorf("New(%v) = %s, want %s", dag.StepType(tt.step), got, tt.want)
		}
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

func TestWrapErrorOn(t *testing.T) {
	code := "cat('hello')"

	// No wrapping for default/error
	if got := wrapErrorOn(code, ""); got != code {
		t.Error("empty error_on should not wrap")
	}
	if got := wrapErrorOn(code, "error"); got != code {
		t.Error("error_on=error should not wrap")
	}

	// Warning wrapping
	wrapped := wrapErrorOn(code, "warning")
	if !strings.Contains(wrapped, "withCallingHandlers") {
		t.Error("error_on=warning should wrap in withCallingHandlers")
	}
	if !strings.Contains(wrapped, "warning = function") {
		t.Error("error_on=warning should include warning handler")
	}
	if strings.Contains(wrapped, "message = function") {
		t.Error("error_on=warning should NOT include message handler")
	}

	// Message wrapping (includes both warning and message handlers)
	wrapped = wrapErrorOn(code, "message")
	if !strings.Contains(wrapped, "withCallingHandlers") {
		t.Error("error_on=message should wrap in withCallingHandlers")
	}
	if !strings.Contains(wrapped, "message = function") {
		t.Error("error_on=message should include message handler")
	}
	if !strings.Contains(wrapped, "warning = function") {
		t.Error("error_on=message should also include warning handler")
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
