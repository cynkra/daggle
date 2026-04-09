package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CleanupResult holds the outcome of a cleanup operation.
type CleanupResult struct {
	Removed    int
	FreedBytes int64
}

// CleanupRuns removes run directories older than the given duration.
func CleanupRuns(olderThan time.Duration) (CleanupResult, error) {
	var result CleanupResult

	runsDir := RunsDir()
	if _, err := os.Stat(runsDir); os.IsNotExist(err) {
		return result, nil
	}

	cutoff := time.Now().Add(-olderThan)

	dagDirs, err := os.ReadDir(runsDir)
	if err != nil {
		return result, fmt.Errorf("read runs directory: %w", err)
	}

	for _, dagDir := range dagDirs {
		if !dagDir.IsDir() {
			continue
		}
		dagPath := filepath.Join(runsDir, dagDir.Name())
		dateDirs, err := os.ReadDir(dagPath)
		if err != nil {
			continue
		}
		for _, dateDir := range dateDirs {
			if !dateDir.IsDir() {
				continue
			}
			datePath := filepath.Join(dagPath, dateDir.Name())
			runDirs, err := os.ReadDir(datePath)
			if err != nil {
				continue
			}
			for _, runDir := range runDirs {
				if !runDir.IsDir() || !strings.HasPrefix(runDir.Name(), "run_") {
					continue
				}
				runPath := filepath.Join(datePath, runDir.Name())
				info, err := runDir.Info()
				if err != nil {
					continue
				}
				if info.ModTime().Before(cutoff) {
					size := dirSize(runPath)
					if err := os.RemoveAll(runPath); err != nil {
						continue
					}
					result.Removed++
					result.FreedBytes += size
				}
			}
			// Remove empty date directories
			remaining, _ := os.ReadDir(datePath)
			if len(remaining) == 0 {
				_ = os.Remove(datePath)
			}
		}
		// Remove empty DAG directories
		remaining, _ := os.ReadDir(dagPath)
		if len(remaining) == 0 {
			_ = os.Remove(dagPath)
		}
	}

	return result, nil
}

func dirSize(path string) int64 {
	var size int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		size += info.Size()
		return nil
	})
	return size
}

// ParseDurationWithDays extends time.ParseDuration to support "d" suffix for days.
func ParseDurationWithDays(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		numStr := strings.TrimSuffix(s, "d")
		var days float64
		if _, err := fmt.Sscanf(numStr, "%f", &days); err != nil {
			return 0, fmt.Errorf("invalid day count %q", numStr)
		}
		return time.Duration(days * float64(24*time.Hour)), nil
	}
	return time.ParseDuration(s)
}
