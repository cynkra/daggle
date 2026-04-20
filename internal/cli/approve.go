package cli

import (
	"fmt"

	"github.com/cynkra/daggle/internal/executor"
	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var approveRunID string

var approveCmd = &cobra.Command{
	Use:   "approve <dag-name>",
	Short: "Approve a waiting DAG run to continue execution",
	Args:  cobra.ExactArgs(1),
	RunE:  runApprove,
}

func init() {
	approveCmd.Flags().StringVar(&approveRunID, "run-id", "", "specific run ID to approve (default: latest)")
	rootCmd.AddCommand(approveCmd)
}

func runApprove(_ *cobra.Command, args []string) error {
	return handleApproval(args[0], approveRunID, true)
}

var rejectRunID string

var rejectCmd = &cobra.Command{
	Use:   "reject <dag-name>",
	Short: "Reject a waiting DAG run",
	Args:  cobra.ExactArgs(1),
	RunE:  runReject,
}

func init() {
	rejectCmd.Flags().StringVar(&rejectRunID, "run-id", "", "specific run ID to reject (default: latest)")
	rootCmd.AddCommand(rejectCmd)
}

func runReject(_ *cobra.Command, args []string) error {
	return handleApproval(args[0], rejectRunID, false)
}

func handleApproval(dagName, runID string, approved bool) error {
	applyOverrides()

	run, err := state.FindRun(dagName, runID)
	if err != nil {
		return err
	}

	status := state.RunStatus(run.Dir)
	if status != "waiting" && status != "running" {
		return fmt.Errorf("run %s is not waiting for approval (status: %s)", run.ID, status)
	}

	// Find which step is waiting for approval
	events, err := state.ReadEvents(run.Dir)
	if err != nil {
		return fmt.Errorf("read events: %w", err)
	}

	var waitingStepID string
	for _, e := range events {
		if e.Type == state.EventStepWaitingApproval {
			waitingStepID = e.StepID
		}
		if e.Type == state.EventStepApproved || e.Type == state.EventStepRejected {
			waitingStepID = "" // already handled
		}
	}

	if waitingStepID == "" {
		return fmt.Errorf("no step is currently waiting for approval in run %s", run.ID)
	}

	if err := executor.WriteApprovalEvent(run.Dir, waitingStepID, approved); err != nil {
		return fmt.Errorf("write approval event: %w", err)
	}

	action := "Approved"
	if !approved {
		action = "Rejected"
	}
	fmt.Printf("%s step %q in DAG %q run %s\n", action, waitingStepID, dagName, run.ID)
	return nil
}

