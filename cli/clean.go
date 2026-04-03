package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var cleanOlderThan string

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove old run directories and logs",
	RunE:  runClean,
}

func init() {
	cleanCmd.Flags().StringVar(&cleanOlderThan, "older-than", "", "remove runs older than this duration (e.g. 30d, 24h)")
	_ = cleanCmd.MarkFlagRequired("older-than")
	rootCmd.AddCommand(cleanCmd)
}

func runClean(_ *cobra.Command, _ []string) error {
	applyOverrides()

	threshold, err := parseDurationWithDays(cleanOlderThan)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", cleanOlderThan, err)
	}

	cutoff := time.Now().Add(-threshold)
	runsDir := state.RunsDir()

	if _, err := os.Stat(runsDir); os.IsNotExist(err) {
		fmt.Println("No runs directory found.")
		return nil
	}

	var removed int
	var freedBytes int64

	// Walk DAG directories
	dagDirs, err := os.ReadDir(runsDir)
	if err != nil {
		return fmt.Errorf("read runs directory: %w", err)
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
						fmt.Printf("Warning: could not remove %s: %v\n", runPath, err)
						continue
					}
					removed++
					freedBytes += size
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

	if removed == 0 {
		fmt.Printf("No runs older than %s found.\n", cleanOlderThan)
	} else {
		fmt.Printf("Removed %d run(s), freed %s\n", removed, formatBytes(freedBytes))
	}
	return nil
}

// parseDurationWithDays extends time.ParseDuration to support "d" suffix for days.
func parseDurationWithDays(s string) (time.Duration, error) {
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

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
