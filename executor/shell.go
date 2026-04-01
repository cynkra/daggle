package executor

import (
	"context"
	"os/exec"

	"github.com/schochastics/rdag/dag"
)

// ShellExecutor runs a shell command via sh -c.
type ShellExecutor struct{}

func (e *ShellExecutor) Run(ctx context.Context, step dag.Step, logDir string, env []string) Result {
	cmd := exec.CommandContext(ctx, "sh", "-c", step.Command)
	return runProcess(ctx, cmd, step.ID, logDir, env)
}
