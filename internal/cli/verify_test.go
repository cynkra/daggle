package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cynkra/daggle/internal/archive"
)

func TestVerifyCmd_ReportsOK(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "x.tar.gz")
	if _, err := archive.Create(src, out); err != nil {
		t.Fatalf("create archive: %v", err)
	}

	rootCmd.SetArgs([]string{"verify", out})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVerifyCmd_FailsOnMissingArchive(t *testing.T) {
	rootCmd.SetArgs([]string{"verify", "/nonexistent/path/to/x.tar.gz"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing archive")
	}
}
