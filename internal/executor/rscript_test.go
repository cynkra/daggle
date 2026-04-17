package executor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cynkra/daggle/dag"
)

func TestWrapRCodeWithSessionInfo_Shape(t *testing.T) {
	wrapped := wrapRCodeWithSessionInfo(`cat("hi")`, "/tmp/run", "step1")

	must := []string{
		`.daggle_session_path <- '/tmp/run/step1.sessioninfo.json'`,
		"tryCatch({",
		`cat("hi")`,
		".daggle_write_sessioninfo(e)",
		"stop(e)",
	}
	for _, s := range must {
		if !strings.Contains(wrapped, s) {
			t.Errorf("wrapped code missing %q\n---\n%s\n---", s, wrapped)
		}
	}
}

func TestRStringLiteral_Escapes(t *testing.T) {
	cases := map[string]string{
		`/simple/path`:   `'/simple/path'`,
		`with'quote`:     `'with\'quote'`,
		`back\slash`:     `'back\\slash'`,
		`mixed\'both`:    `'mixed\\\'both'`,
	}
	for in, want := range cases {
		got := rStringLiteral(in)
		if got != want {
			t.Errorf("rStringLiteral(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRScript_SessionInfoOnFailure is an integration test. Skipped if Rscript
// is not available. Verifies that a failing R step produces a
// {step}.sessioninfo.json file.
func TestRScript_SessionInfoOnFailure(t *testing.T) {
	if _, err := exec.LookPath("Rscript"); err != nil {
		t.Skip("Rscript not available; skipping integration test")
	}

	tmp := t.TempDir()
	step := dag.Step{ID: "boom"}
	res := runRScript(context.Background(),
		`stop("intentional failure")`,
		step, tmp, tmp, nil, "inline",
	)
	if res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got %d", res.ExitCode)
	}

	path := filepath.Join(tmp, "boom.sessioninfo.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("sessioninfo file not written: %v", err)
	}
	content := string(data)
	for _, k := range []string{`"r_version"`, `"platform"`, `"error_message"`, `"session_info"`, `"timestamp"`} {
		if !strings.Contains(content, k) {
			t.Errorf("sessioninfo missing key %s\n%s", k, content)
		}
	}
	if !strings.Contains(content, "intentional failure") {
		t.Errorf("sessioninfo missing original error message\n%s", content)
	}
}

// TestRScript_NoSessionInfoOnSuccess ensures the file is not created when the
// step succeeds.
func TestRScript_NoSessionInfoOnSuccess(t *testing.T) {
	if _, err := exec.LookPath("Rscript"); err != nil {
		t.Skip("Rscript not available; skipping integration test")
	}
	tmp := t.TempDir()
	step := dag.Step{ID: "ok"}
	res := runRScript(context.Background(),
		`cat("hi\n")`,
		step, tmp, tmp, nil, "inline",
	)
	if res.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d err=%v", res.ExitCode, res.Err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "ok.sessioninfo.json")); err == nil {
		t.Error("sessioninfo file should not exist on success")
	}
}
