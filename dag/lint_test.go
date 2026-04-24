package dag_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cynkra/daggle/dag"
)

// writeDAG is a small helper that writes `yaml` to a temp file and returns its
// absolute path plus the parsed DAG. Fails the test on any error.
func writeDAG(t *testing.T, yaml string) (string, *dag.DAG) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pipe.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write dag: %v", err)
	}
	abs, _ := filepath.Abs(path)
	d, err := dag.ParseFile(abs)
	if err != nil {
		t.Fatalf("parse dag: %v", err)
	}
	return abs, d
}

func TestLint_MissingScript(t *testing.T) {
	abs, d := writeDAG(t, "name: t\nsteps:\n  - id: s\n    script: analysis.R\n")
	diags := dag.Lint(d, abs, nil)
	if len(diags) == 0 {
		t.Fatal("expected a missing-script diagnostic, got none")
	}
	got := diags[0]
	if got.Code != "missing-script" || got.Severity != "error" {
		t.Errorf("got %+v, want code=missing-script severity=error", got)
	}
}

func TestLint_AllClean(t *testing.T) {
	abs, d := writeDAG(t, "name: t\nsteps:\n  - id: s\n    command: echo hi\n")
	if diags := dag.Lint(d, abs, nil); len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %+v", diags)
	}
}

func TestLint_UnresolvedEnvSecret(t *testing.T) {
	t.Setenv("DAGGLE_LINT_TEST_MISSING", "")
	if err := os.Unsetenv("DAGGLE_LINT_TEST_MISSING"); err != nil {
		t.Fatal(err)
	}
	abs, d := writeDAG(t,
		"name: t\n"+
			"env:\n  API_KEY: ${env:DAGGLE_LINT_TEST_MISSING}\n"+
			"steps:\n  - id: s\n    command: echo hi\n")
	diags := dag.Lint(d, abs, nil)
	found := false
	for _, x := range diags {
		if x.Code == "unresolved-secret" && strings.Contains(x.Message, "DAGGLE_LINT_TEST_MISSING") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unresolved-secret diagnostic, got %+v", diags)
	}
}

func TestLint_ResolvedEnvSecretNoError(t *testing.T) {
	t.Setenv("DAGGLE_LINT_TEST_SET", "value")
	abs, d := writeDAG(t,
		"name: t\n"+
			"env:\n  API_KEY: ${env:DAGGLE_LINT_TEST_SET}\n"+
			"steps:\n  - id: s\n    command: echo hi\n")
	for _, x := range dag.Lint(d, abs, nil) {
		if x.Code == "unresolved-secret" {
			t.Errorf("did not expect unresolved-secret for resolved env, got %+v", x)
		}
	}
}

func TestLint_NotifyChannels(t *testing.T) {
	abs, d := writeDAG(t,
		"name: t\n"+
			"on_success:\n  notify: slack-team\n"+
			"steps:\n  - id: s\n    command: echo hi\n")

	// Without channels, lint must not flag the reference.
	if diags := dag.Lint(d, abs, nil); len(diags) != 0 {
		t.Errorf("nil channels should skip notify check, got %+v", diags)
	}

	// With a config that doesn't define slack-team, we should get one error.
	channels := &dag.NotifyChannels{
		All:  map[string]bool{"ops-email": true},
		SMTP: map[string]bool{"ops-email": true},
	}
	diags := dag.Lint(d, abs, channels)
	if len(diags) != 1 || diags[0].Code != "unknown-channel" {
		t.Errorf("expected one unknown-channel diagnostic, got %+v", diags)
	}
}

func TestLint_DiagnoseParseError(t *testing.T) {
	diags := dag.DiagnoseParseError("/tmp/x.yaml", &parseErr{msg: "line 1: bad\nline 2: worse"})
	if len(diags) != 2 {
		t.Fatalf("expected 2 diagnostics, got %d: %+v", len(diags), diags)
	}
	for _, x := range diags {
		if x.Severity != "error" || x.Code != "parse" {
			t.Errorf("each diagnostic should be parse/error, got %+v", x)
		}
	}
}

type parseErr struct{ msg string }

func (e *parseErr) Error() string { return e.msg }
