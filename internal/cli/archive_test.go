package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cynkra/daggle/internal/archive"
	"github.com/cynkra/daggle/state"
)

func TestArchiveCmd_CreatesVerifiableArchive(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", dataDir)

	run, err := state.CreateRun("demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run.Dir, "events.jsonl"), []byte(`{"type":"run_started"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run.Dir, "meta.json"), []byte(`{"run_id":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "demo.tar.gz")
	archiveOutput = out
	t.Cleanup(func() { archiveOutput = "" })

	rootCmd.SetArgs([]string{"archive", "demo", run.ID, "-o", out})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("archive: %v", err)
	}

	if _, err := os.Stat(out); err != nil {
		t.Fatalf("archive not created at %s: %v", out, err)
	}

	// Confirm verification via the package directly — the verify CLI is
	// covered by its own test.
	report, err := archive.Verify(out)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !report.OK() {
		t.Errorf("verify failed: %+v", report)
	}
	if report.Files < 2 {
		t.Errorf("expected >=2 files, got %d", report.Files)
	}
}
