//go:build linux || darwin

package executor

import (
	"context"
	"os/exec"
	"testing"

	"github.com/cynkra/daggle/dag"
)

// TestShellRusage verifies peak RSS and CPU are captured on Linux/macOS.
// Also guards against unit-mismatch regressions — a trivial shell process
// should use well under 1 GB, so a result in the hundreds of MB or more
// points at the macOS bytes→KB conversion being dropped (or Linux returning
// bytes by mistake).
func TestShellRusage(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	tmp := t.TempDir()
	step := dag.Step{ID: "shellrss", Command: `printf 'hello\n'`}
	res := (&ShellExecutor{}).Run(context.Background(), step, tmp, tmp, nil)

	if res.ExitCode != 0 {
		t.Fatalf("exit = %d err=%v", res.ExitCode, res.Err)
	}
	if res.PeakRSSKB <= 0 {
		t.Errorf("PeakRSSKB = %d, want > 0", res.PeakRSSKB)
	}
	// A trivial `printf` process should use under 1 GB of RSS on any
	// reasonable platform. If it doesn't, the macOS Maxrss-in-bytes
	// normalization is probably off by 1024×.
	const oneGBinKB int64 = 1024 * 1024
	if res.PeakRSSKB > oneGBinKB {
		t.Errorf("PeakRSSKB = %d KB (> 1 GB) — likely a unit conversion bug", res.PeakRSSKB)
	}
	if res.UserCPUSec+res.SysCPUSec < 0 {
		t.Errorf("CPU totals negative: user=%f sys=%f", res.UserCPUSec, res.SysCPUSec)
	}
}
