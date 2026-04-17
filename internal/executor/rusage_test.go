//go:build linux || darwin

package executor

import (
	"context"
	"os/exec"
	"testing"

	"github.com/cynkra/daggle/dag"
)

// TestShellRusage verifies peak RSS and CPU are captured on Linux/macOS.
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
	if res.UserCPUSec+res.SysCPUSec < 0 {
		t.Errorf("CPU totals negative: user=%f sys=%f", res.UserCPUSec, res.SysCPUSec)
	}
}
