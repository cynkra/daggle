package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/cynkra/daggle/dag"
)

// Tests for the pure lint logic live in dag/lint_test.go. The tests here cover
// the CLI-layer output formatters (emitLintJSON, emitLintGNU) that turn
// dag.Diagnostic values into text for terminals and editor integrations.

func TestLint_JSONFormat(t *testing.T) {
	diags := []dag.Diagnostic{
		{Path: "/tmp/a.yaml", Line: 1, Severity: "error", Code: "x", Message: "y"},
	}

	buf, err := captureStdout(t, func() error { return emitLintJSON(diags) })
	if err != nil {
		t.Fatal(err)
	}

	var got []dag.Diagnostic
	if err := json.Unmarshal([]byte(buf), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nbuf=%s", err, buf)
	}
	if len(got) != 1 || got[0].Code != "x" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestLint_GNUFormat(t *testing.T) {
	diags := []dag.Diagnostic{
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
