package scheduler

import (
	"os"
	"runtime"
	"testing"
)

func TestWritePID_Mode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions only")
	}
	t.Setenv("DAGGLE_DATA_DIR", t.TempDir())
	if err := WritePID(); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	t.Cleanup(func() { _ = RemovePID() })

	info, err := os.Stat(PIDPath())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Errorf("PID file mode = %#o, want %#o", got, want)
	}
}
