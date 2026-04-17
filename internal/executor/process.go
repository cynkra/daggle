package executor

import (
	"bufio"
	"context"
	"errors"
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

const (
	defaultGracePeriod       = 5 * time.Second
	defaultErrorContextLines = 50
	errorDetailMaxLen        = 500
)

// GracePeriod is the time allowed for a process to exit after signal.
// Set via config; falls back to defaultGracePeriod.
var GracePeriod = defaultGracePeriod

// ErrorContextLines is the number of stderr lines to include in error details.
// Set via config; falls back to defaultErrorContextLines.
var ErrorContextLines = defaultErrorContextLines

var (
	// OutputMarkerRe matches ::daggle-output name=<key>::<value> markers in step stdout.
	OutputMarkerRe  = regexp.MustCompile(`^::daggle-output name=([a-zA-Z_][a-zA-Z0-9_]*)::(.*)$`)
	summaryMarkerRe = regexp.MustCompile(`^::daggle-summary format=([a-zA-Z]+)::(.*)$`)
	metaMarkerRe       = regexp.MustCompile(`^::daggle-meta type=([a-zA-Z]+) name=([a-zA-Z_][a-zA-Z0-9_]*)::(.*)$`)
	validationMarkerRe = regexp.MustCompile(`^::daggle-validation status=(pass|warn|fail) name=([a-zA-Z_][a-zA-Z0-9_]*)::(.*)$`)
)

// runProcess executes a command, captures stdout/stderr to log files, and enforces
// timeout via the provided context. On cancellation, it sends SIGTERM to the process
// group, waits a grace period, then sends SIGKILL.
// It also parses ::daggle-output:: markers from stdout.
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
	defer func() { _ = stdoutFile.Close() }()

	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		return Result{ExitCode: -1, Err: err, Duration: time.Since(start)}
	}
	defer func() { _ = stderrFile.Close() }()

	// Use pipes so we can parse stdout line-by-line for ::daggle-output:: markers
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return Result{ExitCode: -1, Err: err, Duration: time.Since(start)}
	}
	cmd.Stderr = io.MultiWriter(stderrFile, os.Stderr)

	if err := cmd.Start(); err != nil {
		return Result{ExitCode: -1, Err: err, Duration: time.Since(start), Stdout: stdoutPath, Stderr: stderrPath}
	}

	// Read stdout, parse output/summary/meta/validation markers, write to log file and terminal
	outputs := make(map[string]string)
	var summaries []Summary
	var metadata []MetaEntry
	var validations []ValidationResult
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			if m := OutputMarkerRe.FindStringSubmatch(line); m != nil {
				outputs[m[1]] = strings.TrimSpace(m[2])
				// Don't write marker lines to terminal, but still log them
				_, _ = fmt.Fprintln(stdoutFile, line)
			} else if m := summaryMarkerRe.FindStringSubmatch(line); m != nil {
				summaries = append(summaries, Summary{
					Format:  m[1],
					Content: m[2],
				})
				_, _ = fmt.Fprintln(stdoutFile, line)
			} else if m := metaMarkerRe.FindStringSubmatch(line); m != nil {
				metadata = append(metadata, MetaEntry{
					Type:  m[1],
					Name:  m[2],
					Value: m[3],
				})
				_, _ = fmt.Fprintln(stdoutFile, line)
			} else if m := validationMarkerRe.FindStringSubmatch(line); m != nil {
				validations = append(validations, ValidationResult{
					Status:  m[1],
					Name:    m[2],
					Message: m[3],
				})
				_, _ = fmt.Fprintln(stdoutFile, line)
			} else {
				_, _ = fmt.Fprintln(stdoutFile, line)
				_, _ = fmt.Fprintln(os.Stdout, line)
			}
		}
	}()

	// Wait for completion in a goroutine so we can handle context cancellation
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		<-scanDone // ensure all stdout is read
		_ = stdoutFile.Sync()
		_ = stderrFile.Sync()
		r := buildResult(err, start, stdoutPath, stderrPath)
		r.Outputs = outputs
		r.Summaries = summaries
		r.Metadata = metadata
		r.Validations = validations
		r.PeakRSSKB, r.UserCPUSec, r.SysCPUSec = extractRusage(cmd)
		return r
	case <-ctx.Done():
		killProcessGroup(cmd)
		<-done
		<-scanDone
		_ = stdoutFile.Sync()
		_ = stderrFile.Sync()
		return Result{
			ExitCode:    -1,
			Err:         ctx.Err(),
			Duration:    time.Since(start),
			Stdout:      stdoutPath,
			Stderr:      stderrPath,
			Outputs:     outputs,
			Summaries:   summaries,
			Metadata:    metadata,
			Validations: validations,
		}
	}
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pgid := -cmd.Process.Pid
	_ = syscall.Kill(pgid, syscall.SIGTERM)

	// Poll for process exit instead of blocking the full grace period
	deadline := time.After(GracePeriod)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			_ = syscall.Kill(pgid, syscall.SIGKILL)
			return
		case <-ticker.C:
			// Check if process has exited (signal 0 returns error if gone)
			if err := syscall.Kill(-pgid, 0); err != nil {
				return // process group already exited
			}
		}
	}
}

func buildResult(err error, start time.Time, stdoutPath, stderrPath string) Result {
	r := Result{
		Duration: time.Since(start),
		Stdout:   stdoutPath,
		Stderr:   stderrPath,
	}
	if err != nil {
		r.Err = err
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			r.ExitCode = exitErr.ExitCode()
		} else {
			r.ExitCode = -1
		}
		r.ErrorDetail = extractErrorDetail(stderrPath)
	}
	return r
}

var (
	rErrorRe      = regexp.MustCompile(`^Error[ :]`)
	quartoErrorRe = regexp.MustCompile(`(?i)^ERROR:`)
)

// extractErrorDetail reads the last lines of a stderr log file and extracts
// a meaningful error message. It tries R errors first, then Quarto errors,
// then falls back to the last non-empty stderr lines.
func extractErrorDetail(stderrPath string) string {
	if stderrPath == "" {
		return ""
	}
	data, err := os.ReadFile(stderrPath)
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")

	// Try R error extraction
	if detail := extractPatternError(lines, rErrorRe, "Execution halted"); detail != "" {
		return detail
	}

	// Try Quarto error extraction
	if detail := extractPatternError(lines, quartoErrorRe, ""); detail != "" {
		return detail
	}

	// Fallback: last non-empty stderr lines (for shell commands and other tools)
	return extractLastLines(lines, 5)
}

// extractPatternError searches the last 50 lines for a matching error pattern
// and returns the error line plus following context.
func extractPatternError(lines []string, pattern *regexp.Regexp, skipLine string) string {
	errorStart := -1
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-ErrorContextLines; i-- {
		line := strings.TrimSpace(lines[i])
		if pattern.MatchString(line) {
			errorStart = i
			break
		}
	}
	if errorStart < 0 {
		return ""
	}

	var errorLines []string
	for i := errorStart; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if skipLine != "" && line == skipLine {
			continue
		}
		if line != "" {
			errorLines = append(errorLines, line)
		}
	}

	return truncateDetail(strings.Join(errorLines, "\n"))
}

// extractLastLines returns the last n non-empty lines from stderr.
func extractLastLines(lines []string, n int) string {
	var result []string
	for i := len(lines) - 1; i >= 0 && len(result) < n; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			result = append([]string{line}, result...)
		}
	}
	return truncateDetail(strings.Join(result, "\n"))
}

func truncateDetail(s string) string {
	if len(s) > errorDetailMaxLen {
		return s[:errorDetailMaxLen-3] + "..."
	}
	return s
}
