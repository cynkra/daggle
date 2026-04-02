package scheduler

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/cynkra/daggle/state"
)

// PIDPath returns the path to the scheduler PID file.
func PIDPath() string {
	return filepath.Join(state.DataDir(), "proc", "scheduler.pid")
}

// WritePID writes the current process PID to the PID file.
func WritePID() error {
	path := PIDPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create proc dir: %w", err)
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0644)
}

// ReadPID reads the PID from the PID file.
func ReadPID() (int, error) {
	data, err := os.ReadFile(PIDPath())
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// RemovePID removes the PID file.
func RemovePID() error {
	return os.Remove(PIDPath())
}

// IsRunning checks if a scheduler process is already running.
func IsRunning() bool {
	pid, err := ReadPID()
	if err != nil {
		return false
	}
	// Check if process exists (signal 0 doesn't send anything, just checks)
	err = syscall.Kill(pid, 0)
	return err == nil
}
