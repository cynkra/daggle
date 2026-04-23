package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLint_MissingScript(t *testing.T) {
	dir := t.TempDir()
	dagPath := filepath.Join(dir, "pipe.yaml")
	yaml := "name: t\nsteps:\n  - id: s\n    script: analysis.R\n"
	if err := os.WriteFile(dagPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	abs, _ := filepath.Abs(dagPath)
	diags := collectLintDiagnostics(abs)

	if len(diags) == 0 {
		t.Fatal("expected a missing-script diagnostic, got none")
	}
	got := diags[0]
	if got.Code != "missing-script" || got.Severity != "error" {
		t.Errorf("got %+v, want code=missing-script severity=error", got)
	}
}

func TestLint_AllClean(t *testing.T) {
	dir := t.TempDir()
	dagPath := filepath.Join(dir, "pipe.yaml")
	yaml := "name: t\nsteps:\n  - id: s\n    command: echo hi\n"
	if err := os.WriteFile(dagPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	abs, _ := filepath.Abs(dagPath)
	diags := collectLintDiagnostics(abs)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %+v", diags)
	}
}

func TestLint_UnresolvedEnvSecret(t *testing.T) {
	// Ensure the env var is NOT set. t.Setenv restores at test end.
	t.Setenv("DAGGLE_LINT_TEST_MISSING", "")
	if err := os.Unsetenv("DAGGLE_LINT_TEST_MISSING"); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	dagPath := filepath.Join(dir, "pipe.yaml")
	yaml := "name: t\n" +
		"env:\n  API_KEY: ${env:DAGGLE_LINT_TEST_MISSING}\n" +
		"steps:\n  - id: s\n    command: echo hi\n"
	if err := os.WriteFile(dagPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	abs, _ := filepath.Abs(dagPath)
	diags := collectLintDiagnostics(abs)

	found := false
	for _, d := range diags {
		if d.Code == "unresolved-secret" && strings.Contains(d.Message, "DAGGLE_LINT_TEST_MISSING") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unresolved-secret diagnostic, got %+v", diags)
	}
}

func TestLint_ResolvedEnvSecretNoError(t *testing.T) {
	t.Setenv("DAGGLE_LINT_TEST_SET", "value")

	dir := t.TempDir()
	dagPath := filepath.Join(dir, "pipe.yaml")
	yaml := "name: t\n" +
		"env:\n  API_KEY: ${env:DAGGLE_LINT_TEST_SET}\n" +
		"steps:\n  - id: s\n    command: echo hi\n"
	if err := os.WriteFile(dagPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	abs, _ := filepath.Abs(dagPath)
	diags := collectLintDiagnostics(abs)
	for _, d := range diags {
		if d.Code == "unresolved-secret" {
			t.Errorf("did not expect unresolved-secret for resolved env, got %+v", d)
		}
	}
}

func TestLint_JSONFormat(t *testing.T) {
	diags := []lintDiagnostic{
		{Path: "/tmp/a.yaml", Line: 1, Severity: "error", Code: "x", Message: "y"},
	}

	buf, err := captureStdout(t, func() error { return emitLintJSON(diags) })
	if err != nil {
		t.Fatal(err)
	}

	var got []lintDiagnostic
	if err := json.Unmarshal([]byte(buf), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nbuf=%s", err, buf)
	}
	if len(got) != 1 || got[0].Code != "x" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestLint_GNUFormat(t *testing.T) {
	diags := []lintDiagnostic{
		{Path: "/tmp/a.yaml", Severity: "error", Code: "x", Message: "y"},
	}

	buf, err := captureStdout(t, func() error { return emitLintGNU(diags) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf, "/tmp/a.yaml:1:1: error: y [x]") {
		t.Errorf("expected GNU-style line, got: %q", buf)
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns what
// was written. Used by tests that exercise emit* helpers.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = w
	done := make(chan struct{})
	var buf strings.Builder
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := r.Read(b)
			if n > 0 {
				buf.Write(b[:n])
			}
			if err != nil {
				break
			}
		}
		close(done)
	}()
	callErr := fn()
	_ = w.Close()
	<-done
	os.Stdout = orig
	return buf.String(), callErr
}
