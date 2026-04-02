package cli

import (
	"fmt"
	"os"
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

func showStatus(cmd *cobra.Command, args []string) error {
	dagName := args[0]
	applyOverrides()

	var run *state.RunInfo

	if statusRunID != "" {
		// Find specific run
		runs, err := state.ListRuns(dagName)
		if err != nil {
			return err
		}
		for _, r := range runs {
			if r.ID == statusRunID {
				run = &r
				break
			}
		}
		if run == nil {
			return fmt.Errorf("run %q not found for DAG %q", statusRunID, dagName)
		}
	} else {
		var err error
		run, err = state.LatestRun(dagName)
		if err != nil {
			return err
		}
		if run == nil {
			fmt.Printf("No runs found for DAG %q\n", dagName)
			return nil
		}
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
	fmt.Printf("Status: %s\n\n", status)

	// Build step summary from events
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "STEP\tSTATUS\tDURATION\tATTEMPTS\tERROR")

	type stepInfo struct {
		status   string
		duration string
		attempts int
		err      string
	}
	steps := make(map[string]*stepInfo)

	for _, e := range events {
		if e.StepID == "" {
			continue
		}
		si, ok := steps[e.StepID]
		if !ok {
			si = &stepInfo{}
			steps[e.StepID] = si
		}
		switch e.Type {
		case state.EventStepStarted:
			si.status = "running"
			si.attempts = e.Attempt
		case state.EventStepCompleted:
			si.status = "completed"
			si.duration = e.Duration
			si.attempts = e.Attempt
		case state.EventStepFailed:
			si.status = "failed"
			si.duration = e.Duration
			si.err = e.Error
			si.attempts = e.Attempt
		case state.EventStepRetrying:
			si.status = "retrying"
		}
	}

	// Print in order of first appearance
	seen := make(map[string]bool)
	for _, e := range events {
		if e.StepID == "" || seen[e.StepID] {
			continue
		}
		seen[e.StepID] = true
		si := steps[e.StepID]
		errStr := si.err
		if len(errStr) > 40 {
			errStr = errStr[:37] + "..."
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", e.StepID, si.status, si.duration, si.attempts, errStr)
	}
	_ = w.Flush()

	return nil
}
