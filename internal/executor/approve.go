package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"time"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
)

// ApproveExecutor blocks until a human approves or rejects via CLI.
type ApproveExecutor struct{}

// Run writes a waiting_approval event and polls for approval/rejection.
func (e *ApproveExecutor) Run(ctx context.Context, step dag.Step, logDir string, _ string, _ []string) Result {
	start := time.Now()
	a := step.Approve

	// Write waiting event
	writer := state.NewEventWriter(logDir)
	defer func() { _ = writer.Close() }()
	_ = writer.Write(state.Event{
		Type:    state.EventStepWaitingApproval,
		StepID:  step.ID,
		Message: a.Message,
	})

	// Run notify hook if present
	if a.Notify != nil {
		runApprovalNotify(ctx, a.Notify, logDir, step.ID)
	}

	// Parse timeout
	var deadline <-chan time.Time
	if a.Timeout != "" {
		if d, err := time.ParseDuration(a.Timeout); err == nil {
			deadline = time.After(d)
		}
	}

	// Poll for approval/rejection event
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return Result{ExitCode: -1, Err: ctx.Err(), Duration: time.Since(start)}
		case <-deadline:
			return Result{ExitCode: 1, Err: fmt.Errorf("approval timed out after %s", a.Timeout), Duration: time.Since(start)}
		case <-ticker.C:
			decision := checkApprovalDecision(logDir, step.ID)
			switch decision {
			case "approved":
				return Result{ExitCode: 0, Duration: time.Since(start)}
			case "rejected":
				return Result{ExitCode: 1, Err: fmt.Errorf("step rejected"), Duration: time.Since(start)}
			}
		}
	}
}

// checkApprovalDecision reads events to check if the step was approved or rejected.
func checkApprovalDecision(runDir, stepID string) string {
	events, err := state.ReadEvents(runDir)
	if err != nil {
		return ""
	}
	for _, e := range events {
		if e.StepID != stepID {
			continue
		}
		switch e.Type {
		case state.EventStepApproved:
			return "approved"
		case state.EventStepRejected:
			return "rejected"
		}
	}
	return ""
}

func runApprovalNotify(ctx context.Context, hook *dag.Hook, logDir, stepID string) {
	if hook.Command != "" {
		cmd := exec.CommandContext(ctx, state.ToolPath("sh"), "-c", hook.Command)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
	}
	if hook.RExpr != "" {
		tmpFile := filepath.Join(logDir, stepID+".notify.R")
		if err := os.WriteFile(tmpFile, []byte(hook.RExpr), 0o644); err != nil {
			return
		}
		cmd := exec.CommandContext(ctx, state.ToolPath("rscript"), "--no-save", "--no-restore", tmpFile)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		runErr := cmd.Run()
		// Notify hooks are fire-and-forget; remove the temp script on
		// success, keep it on failure for post-mortem.
		if runErr == nil {
			_ = os.Remove(tmpFile)
		}
	}
}

// WriteApprovalEvent writes an approval or rejection event to a run's events.jsonl.
func WriteApprovalEvent(runDir, stepID string, approved bool) error {
	writer := state.NewEventWriter(runDir)
	defer func() { _ = writer.Close() }()
	eventType := state.EventStepApproved
	if !approved {
		eventType = state.EventStepRejected
	}

	approver := "unknown"
	if u, err := user.Current(); err == nil {
		approver = u.Username
	}

	return writer.Write(state.Event{
		Type:     eventType,
		StepID:   stepID,
		Approver: approver,
	})
}
