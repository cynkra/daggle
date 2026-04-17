//go:build darwin

package executor

import (
	"os/exec"
	"syscall"
)

// extractRusage returns peak RSS (KB), user CPU seconds, and system CPU seconds
// from the ProcessState. On macOS, Rusage.Maxrss is in bytes, so we divide by
// 1024 to normalize to KB (matching Linux's unit).
func extractRusage(cmd *exec.Cmd) (int64, float64, float64) {
	if cmd == nil || cmd.ProcessState == nil {
		return 0, 0, 0
	}
	ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok || ru == nil {
		return 0, 0, 0
	}
	user := float64(ru.Utime.Sec) + float64(ru.Utime.Usec)/1e6
	sys := float64(ru.Stime.Sec) + float64(ru.Stime.Usec)/1e6
	return int64(ru.Maxrss) / 1024, user, sys
}
