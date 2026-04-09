package cli

import (
	"fmt"
	"path/filepath"

	"github.com/cynkra/daggle/cache"
	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var (
	cleanOlderThan string
	cleanCache     bool
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove old run directories and logs",
	RunE:  runClean,
}

func init() {
	cleanCmd.Flags().StringVar(&cleanOlderThan, "older-than", "", "remove runs older than this duration (e.g. 30d, 24h)")
	cleanCmd.Flags().BoolVar(&cleanCache, "cache", false, "clear the step-level cache")
	rootCmd.AddCommand(cleanCmd)
}

func runClean(_ *cobra.Command, _ []string) error {
	applyOverrides()

	// If only --cache is requested (no --older-than), just clear cache
	if cleanCache && cleanOlderThan == "" {
		store := cache.NewStore(filepath.Join(state.DataDir(), "cache"))
		if err := store.ClearAll(); err != nil {
			return err
		}
		fmt.Println("Cache cleared.")
		return nil
	}

	if cleanOlderThan == "" {
		return fmt.Errorf("--older-than is required (unless using --cache alone)")
	}

	threshold, err := state.ParseDurationWithDays(cleanOlderThan)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", cleanOlderThan, err)
	}

	result, err := state.CleanupRuns(threshold)
	if err != nil {
		return err
	}

	if result.Removed == 0 {
		fmt.Printf("No runs older than %s found.\n", cleanOlderThan)
	} else {
		fmt.Printf("Removed %d run(s), freed %s\n", result.Removed, formatBytes(result.FreedBytes))
	}

	// Also clear cache if requested alongside normal cleanup
	if cleanCache {
		store := cache.NewStore(filepath.Join(state.DataDir(), "cache"))
		if err := store.ClearAll(); err != nil {
			return err
		}
		fmt.Println("Cache cleared.")
	}

	return nil
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
