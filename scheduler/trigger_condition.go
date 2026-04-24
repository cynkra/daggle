package scheduler

import (
	"context"
	"os/exec"
	"time"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
)

// setupConditionTrigger starts a polling goroutine that evaluates an R expression or
// shell command periodically and triggers a run when it succeeds (exit code 0).
func (s *Scheduler) setupConditionTrigger(ctx context.Context, d *dag.DAG, dagPath string) context.CancelFunc {
	c := d.Trigger.Condition

	pollInterval := 5 * time.Minute
	if c.PollInterval != "" {
		if parsed, err := time.ParseDuration(c.PollInterval); err == nil {
			pollInterval = parsed
		}
	}

	condCtx, cancel := context.WithCancel(ctx)

	s.logger.Info("condition trigger started", "dag", d.Name, "poll_interval", pollInterval)

	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-condCtx.Done():
				return
			case <-ticker.C:
				var cmd *exec.Cmd
				switch {
				case c.Command != "":
					cmd = exec.CommandContext(condCtx, state.ToolPath("sh"), "-c", c.Command)
				case c.RExpr != "":
					cmd = exec.CommandContext(condCtx, state.ToolPath("rscript"), "-e", c.RExpr)
				default:
					continue
				}
				if err := cmd.Run(); err == nil {
					s.logger.Info("condition met, triggering run", "dag", d.Name)
					s.triggerRun(dagPath, "condition")
				}
			}
		}
	}()

	return cancel
}
