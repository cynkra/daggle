package executor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const gracePeriod = 5 * time.Second

var outputMarkerRe = regexp.MustCompile(`^::rdag-output name=([a-zA-Z_][a-zA-Z0-9_]*)::(.*)$`)

// runProcess executes a command, captures stdout/stderr to log files, and enforces
// timeout via the provided context. On cancellation, it sends SIGTERM to the process
// group, waits a grace period, then sends SIGKILL.
// It also parses ::rdag-output:: markers from stdout.
func runProcess(ctx context.Context, cmd *exec.Cmd, stepID, logDir, workdir string, env []string) Result {
	start := time.Now()

	// Set up process group so we can kill children too
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Set working directory
	if workdir != "" {
		cmd.Dir = workdir
	}

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

	// Use pipes so we can parse stdout line-by-line for ::rdag-output:: markers
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return Result{ExitCode: -1, Err: err, Duration: time.Since(start)}
	}
	cmd.Stderr = io.MultiWriter(stderrFile, os.Stderr)

	if err := cmd.Start(); err != nil {
		return Result{ExitCode: -1, Err: err, Duration: time.Since(start), Stdout: stdoutPath, Stderr: stderrPath}
	}

	// Read stdout, parse output markers, write to log file and terminal
	outputs := make(map[string]string)
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			if m := outputMarkerRe.FindStringSubmatch(line); m != nil {
				outputs[m[1]] = strings.TrimSpace(m[2])
				// Don't write marker lines to terminal, but still log them
				fmt.Fprintln(stdoutFile, line)
			} else {
				fmt.Fprintln(stdoutFile, line)
				fmt.Fprintln(os.Stdout, line)
			}
		}
	}()

	// Wait for completion in a goroutine so we can handle context cancellation
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		<-scanDone // ensure all stdout is read
		r := buildResult(err, start, stdoutPath, stderrPath)
		r.Outputs = outputs
		return r
	case <-ctx.Done():
		killProcessGroup(cmd)
		<-done
		<-scanDone
		return Result{
			ExitCode: -1,
			Err:      ctx.Err(),
			Duration: time.Since(start),
			Stdout:   stdoutPath,
			Stderr:   stderrPath,
			Outputs:  outputs,
		}
	}
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pgid := -cmd.Process.Pid
	_ = syscall.Kill(pgid, syscall.SIGTERM)

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
