package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var cancelRunID string

var cancelCmd = &cobra.Command{
	Use:   "cancel <dag-name>",
	Short: "Cancel an in-flight DAG run",
	Args:  cobra.ExactArgs(1),
	RunE:  runCancel,
}

func init() {
	cancelCmd.Flags().StringVar(&cancelRunID, "run-id", "", "specific run ID to cancel (default: latest)")
	rootCmd.AddCommand(cancelCmd)
}

func runCancel(_ *cobra.Command, args []string) error {
	dagName := args[0]
	applyOverrides()

	var run *state.RunInfo

	if cancelRunID != "" {
		runs, err := state.ListRuns(dagName)
		if err != nil {
			return err
		}
		for _, r := range runs {
			if r.ID == cancelRunID {
				run = &r
				break
			}
		}
		if run == nil {
			return fmt.Errorf("run %q not found for DAG %q", cancelRunID, dagName)
		}
	} else {
		var err error
		run, err = state.LatestRun(dagName)
		if err != nil {
			return err
		}
		if run == nil {
			return fmt.Errorf("no runs found for DAG %q", dagName)
		}
	}

	// Check if the run is still active
	status := state.RunStatus(run.Dir)
	if status != "running" {
		return fmt.Errorf("run %s is not running (status: %s)", run.ID, status)
	}

	// Write a cancel request file that the engine checks between tiers
	cancelFile := filepath.Join(run.Dir, "cancel.requested")
	if err := os.WriteFile(cancelFile, []byte("cancel"), 0o644); err != nil {
		return fmt.Errorf("write cancel request: %w", err)
	}

	fmt.Printf("Cancel requested for DAG %q run %s\n", dagName, run.ID)
	fmt.Println("The run will stop after the current tier completes.")
	return nil
}
