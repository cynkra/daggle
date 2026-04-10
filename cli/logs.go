package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var (
	logsRunID  string
	logsStep   string
	logsStderr bool
)

var logsCmd = &cobra.Command{
	Use:   "logs <dag-name>",
	Short: "Show step logs for a run",
	Args:  cobra.ExactArgs(1),
	RunE:  showLogs,
}

func init() {
	logsCmd.Flags().StringVar(&logsRunID, "run-id", "", "specific run ID")
	logsCmd.Flags().StringVar(&logsStep, "step", "", "specific step ID")
	logsCmd.Flags().BoolVar(&logsStderr, "stderr", false, "show stderr instead of stdout")
	rootCmd.AddCommand(logsCmd)
}

func showLogs(_ *cobra.Command, args []string) error {
	dagName := args[0]
	applyOverrides()

	run, err := state.FindRun(dagName, logsRunID)
	if err != nil {
		return err
	}

	ext := ".stdout.log"
	if logsStderr {
		ext = ".stderr.log"
	}

	if logsStep != "" {
		return printStepLog(run.Dir, logsStep, ext)
	}

	// No specific step: print all step logs from events
	events, err := state.ReadEvents(run.Dir)
	if err != nil {
		return fmt.Errorf("read events: %w", err)
	}

	seen := make(map[string]bool)
	for _, e := range events {
		if e.StepID == "" || seen[e.StepID] {
			continue
		}
		seen[e.StepID] = true
		fmt.Printf("=== %s ===\n", e.StepID)
		if err := printStepLog(run.Dir, e.StepID, ext); err != nil {
			fmt.Printf("  (no log file)\n")
		}
		fmt.Println()
	}
	return nil
}

func printStepLog(runDir, stepID, ext string) error {
	path := filepath.Join(runDir, stepID+ext)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}
