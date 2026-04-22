package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cynkra/daggle/state"
)

func writeDAG(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name+".yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunDryRun_ValidDAG_DoesNotCreateRun(t *testing.T) {
	dataDir := t.TempDir()
	dagsDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", dataDir)
	t.Setenv("DAGGLE_DAGS_DIR", dagsDir)

	yaml := `name: dryone
steps:
  - id: a
    command: echo hello
`
	writeDAG(t, dagsDir, "dryone", yaml)

	runDryRun = true
	t.Cleanup(func() { runDryRun = false })

	rootCmd.SetArgs([]string{"run", "dryone", "--dry-run"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("run --dry-run: %v", err)
	}

	runs, _ := state.ListRuns("dryone")
	if len(runs) != 0 {
		t.Errorf("dry-run created %d run(s); expected 0", len(runs))
	}
}

func TestRunDryRun_FailsOnMissingScript(t *testing.T) {
	dataDir := t.TempDir()
	dagsDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", dataDir)
	t.Setenv("DAGGLE_DAGS_DIR", dagsDir)

	yaml := `name: drymissing
steps:
  - id: a
    script: nope.R
`
	writeDAG(t, dagsDir, "drymissing", yaml)

	runDryRun = true
	t.Cleanup(func() { runDryRun = false })

	rootCmd.SetArgs([]string{"run", "drymissing", "--dry-run"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing script")
	}
	if !strings.Contains(err.Error(), "dry-run") {
		t.Errorf("error should mention dry-run, got: %v", err)
	}
}

func TestRunDryRun_FailsOnUnresolvableSecret(t *testing.T) {
	dataDir := t.TempDir()
	dagsDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", dataDir)
	t.Setenv("DAGGLE_DAGS_DIR", dagsDir)

	// ${file:} pointing at a non-existent path causes ResolveEnv to error
	// before the dry-run report runs. This still surfaces as a non-nil
	// error to the user, which is the contract.
	yaml := `name: drysecret
env:
  PASSWORD:
    value: "${file:/nonexistent/secret}"
    secret: true
steps:
  - id: a
    command: echo hi
`
	writeDAG(t, dagsDir, "drysecret", yaml)

	runDryRun = true
	t.Cleanup(func() { runDryRun = false })

	rootCmd.SetArgs([]string{"run", "drysecret", "--dry-run"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for unresolvable secret")
	}
}
