package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var statusRunID string

var statusCmd = &cobra.Command{
	Use:   "status <dag-name>",
	Short: "Show status of the latest (or a specific) run",
	Args:  cobra.ExactArgs(1),
	RunE:  showStatus,
}

func init() {
	statusCmd.Flags().StringVar(&statusRunID, "run-id", "", "specific run ID to show")
	rootCmd.AddCommand(statusCmd)
}

func showStatus(_ *cobra.Command, args []string) error {
	dagName := args[0]
	applyOverrides()

	run, err := state.FindRun(dagName, statusRunID)
	if err != nil {
		return err
	}

	// Read events
	events, err := state.ReadEvents(run.Dir)
	if err != nil {
		return fmt.Errorf("read events: %w", err)
	}

	status := state.RunStatus(run.Dir)
	fmt.Printf("DAG: %s\n", dagName)
	fmt.Printf("Run: %s\n", run.ID)
	fmt.Printf("Started: %s\n", run.StartTime.Format("2006-01-02 15:04:05"))
	fmt.Printf("Status: %s\n", status)

	// Show DAG hash from metadata if available
	if meta, err := state.ReadMeta(run.Dir); err == nil && meta.DAGHash != "" {
		fmt.Printf("DAG hash: %s\n", meta.DAGHash[:12])
	}
	fmt.Println()

	// Build step summary from events
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "STEP\tSTATUS\tDURATION\tATTEMPTS\tERROR")

	summaries := state.BuildStepSummaries(events)
	for _, ss := range summaries {
		errStr := ss.Error
		if ss.Status == "waiting" && ss.Message != "" {
			errStr = ss.Message
		}
		if len(errStr) > 40 {
			errStr = errStr[:37] + "..."
		}
		dur := ""
		if ss.Duration > 0 {
			dur = ss.Duration.String()
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", ss.StepID, ss.Status, dur, ss.Attempts, errStr)
	}
	_ = w.Flush()

	// Show error details for failed steps
	for _, ss := range summaries {
		if ss.ErrorDetail != "" {
			fmt.Printf("\nError detail for step %q:\n  %s\n", ss.StepID, strings.ReplaceAll(ss.ErrorDetail, "\n", "\n  "))
		}
	}

	return nil
}
