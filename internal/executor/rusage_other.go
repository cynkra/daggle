//go:build !linux && !darwin

package executor

import "os/exec"

// extractRusage returns zeros on platforms without syscall.Rusage support.
func extractRusage(cmd *exec.Cmd) (int64, float64, float64) {
	_ = cmd
	return 0, 0, 0
}
