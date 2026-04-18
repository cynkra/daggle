//go:build linux || darwin

package executor

import (
	"context"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/cynkra/daggle/dag"
)

// TestKillProcessGroup_ReapsGroupOnTimeout verifies that cancelling a shell
// step which backgrounds a child process doesn't leak the child. The bug
// this guards against: killProcessGroup polled `-pgid` (which flips the sign
// and checks only the lead PID) instead of the process group, so if the
// lead died before the group, killProcessGroup returned early without
// waiting on the SIGKILL deadline.
//
// Even in the common case (SIGTERM to group kills everyone), this test is a
// cheap regression guard against anyone re-introducing the sign flip.
func TestKillProcessGroup_ReapsGroupOnTimeout(t *testing.T) {
	oldGrace := GracePeriod
	GracePeriod = 500 * time.Millisecond
	defer func() { GracePeriod = oldGrace }()

	logDir := t.TempDir()
	step := dag.Step{
		ID:      "bg-child",
		Command: `sh -c 'sleep 30 & echo "::daggle-output name=child_pid::$!"; wait'`,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	exec := &ShellExecutor{}
	result := exec.Run(ctx, step, logDir, "", nil)
	if result.Err == nil {
		t.Fatal("expected context error from timeout")
	}

	pidStr, ok := result.Outputs["child_pid"]
	if !ok || pidStr == "" {
		t.Fatalf("no child_pid output; outputs=%v", result.Outputs)
	}
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		t.Fatalf("parse child pid %q: %v", pidStr, err)
	}

	deadline := time.Now().Add(GracePeriod + time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return // child gone — group correctly reaped
		}
		time.Sleep(20 * time.Millisecond)
	}

	_ = syscall.Kill(pid, syscall.SIGKILL) // clean up in case of failure
	t.Fatalf("child pid %d survived process-group termination", pid)
}
