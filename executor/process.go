package executor

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const gracePeriod = 5 * time.Second

// runProcess executes a command, captures stdout/stderr to log files, and enforces
// timeout via the provided context. On cancellation, it sends SIGTERM to the process
// group, waits a grace period, then sends SIGKILL.
func runProcess(ctx context.Context, cmd *exec.Cmd, stepID, logDir string, env []string) Result {
	start := time.Now()

	// Set up process group so we can kill children too
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Merge environment
	cmd.Env = append(os.Environ(), env...)

	// Set up log files
	stdoutPath := filepath.Join(logDir, stepID+".stdout.log")
	stderrPath := filepath.Join(logDir, stepID+".stderr.log")

	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		return Result{ExitCode: -1, Err: err, Duration: time.Since(start)}
	}
	defer stdoutFile.Close()

	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		return Result{ExitCode: -1, Err: err, Duration: time.Since(start)}
	}
	defer stderrFile.Close()

	cmd.Stdout = io.MultiWriter(stdoutFile, os.Stdout)
	cmd.Stderr = io.MultiWriter(stderrFile, os.Stderr)

	if err := cmd.Start(); err != nil {
		return Result{ExitCode: -1, Err: err, Duration: time.Since(start), Stdout: stdoutPath, Stderr: stderrPath}
	}

	// Wait for completion in a goroutine so we can handle context cancellation
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return buildResult(err, start, stdoutPath, stderrPath)
	case <-ctx.Done():
		// Timeout or cancellation: kill the process group
		killProcessGroup(cmd)
		<-done // wait for process to actually exit
		return Result{
			ExitCode: -1,
			Err:      ctx.Err(),
			Duration: time.Since(start),
			Stdout:   stdoutPath,
			Stderr:   stderrPath,
		}
	}
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pgid := -cmd.Process.Pid
	// Try graceful shutdown first
	_ = syscall.Kill(pgid, syscall.SIGTERM)

	// Wait for grace period, then force kill
	timer := time.NewTimer(gracePeriod)
	defer timer.Stop()
	<-timer.C
	_ = syscall.Kill(pgid, syscall.SIGKILL)
}

func buildResult(err error, start time.Time, stdoutPath, stderrPath string) Result {
	r := Result{
		Duration: time.Since(start),
		Stdout:   stdoutPath,
		Stderr:   stderrPath,
	}
	if err != nil {
		r.Err = err
		if exitErr, ok := err.(*exec.ExitError); ok {
			r.ExitCode = exitErr.ExitCode()
		} else {
			r.ExitCode = -1
		}
	}
	return r
}
