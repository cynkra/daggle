package cli

import (
	"fmt"

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
