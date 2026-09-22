package executor

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cynkra/daggle/dag"
)

func collect(t *testing.T, r io.Reader) []string {
	t.Helper()
	var got []string
	drainLines(r, func(line string) { got = append(got, line) })
	return got
}

func TestDrainLines_Basics(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"one line", "hello\n", []string{"hello"}},
		{"no trailing newline", "hello", []string{"hello"}},
		{"blank lines kept", "a\n\nb\n", []string{"a", "", "b"}},
		{"crlf stripped", "a\r\nb\r\n", []string{"a", "b"}},
		{"lone cr is not a separator", "a\rb\n", []string{"a\rb"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := collect(t, strings.NewReader(tc.in))
			if len(got) != len(tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("line %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// A line longer than bufio's 64KB default is what a "\r" progress counter
// produces. It must come through as one line, not end the read.
func TestDrainLines_LineOverScannerDefault(t *testing.T) {
	long := strings.Repeat("x", 200*1024)
	got := collect(t, strings.NewReader(long+"\nafter\n"))

	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2", len(got))
	}
	if got[0] != long {
		t.Errorf("long line = %d bytes, want %d", len(got[0]), len(long))
	}
	if got[1] != "after" {
		t.Errorf("line after the long one = %q, want %q", got[1], "after")
	}
}

// Past the cap the excess is dropped, but reading carries on.
func TestDrainLines_TruncatesPastCap(t *testing.T) {
	over := stdoutMaxLineBytes + 5000
	got := collect(t, strings.NewReader(strings.Repeat("y", over)+"\nafter\n"))

	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2", len(got))
	}
	if !strings.HasPrefix(got[0], strings.Repeat("y", 100)) {
		t.Errorf("truncated line lost its start")
	}
	if !strings.Contains(got[0], "line too long") {
		t.Errorf("truncated line not marked: %q", got[0][len(got[0])-80:])
	}
	// Cap, plus the notice; nothing like the 1MB+ the input held.
	if len(got[0]) > stdoutMaxLineBytes+200 {
		t.Errorf("kept %d bytes, want at most the %d cap plus a short notice",
			len(got[0]), stdoutMaxLineBytes)
	}
	if got[1] != "after" {
		t.Errorf("line after the long one = %q, want %q", got[1], "after")
	}
}

// Regression: a step printing far more than 64KB without a newline used to
// wedge the reader, after which the child blocked in write() on a full pipe
// and RunProcess waited on it for ever. The run only ended when it was killed.
func TestShellExecutor_LongLineDoesNotDeadlock(t *testing.T) {
	logDir := t.TempDir()
	// 300KB on one line, then a newline and a marker, so we can tell the
	// difference between "drained" and merely "did not hang".
	step := dag.Step{
		ID: "long-line",
		Command: fmt.Sprintf(
			`printf 'x%%.0s' $(seq 1 %d); printf '\n::daggle-output name=done::yes\n'`,
			300*1024),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	done := make(chan Result, 1)
	go func() { done <- (&ShellExecutor{}).Run(ctx, step, logDir, "", nil) }()

	var result Result
	select {
	case result = <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("step did not finish: the stdout reader stopped and deadlocked the pipe")
	}

	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (err: %v)", result.ExitCode, result.Err)
	}
	if result.Outputs["done"] != "yes" {
		t.Errorf("output after the long line = %q, want %q; stdout was not drained to the end",
			result.Outputs["done"], "yes")
	}

	stdout, err := os.ReadFile(filepath.Join(logDir, "long-line.stdout.log"))
	if err != nil {
		t.Fatalf("read stdout log: %v", err)
	}
	if got := len(stdout); got < 300*1024 {
		t.Errorf("stdout log is %d bytes, want the whole %d-byte line", got, 300*1024)
	}
}
