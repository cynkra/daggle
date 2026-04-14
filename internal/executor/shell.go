package executor

import (
	"context"
	"os/exec"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
)

// ShellExecutor runs a shell command via sh -c.
type ShellExecutor struct{}

// Run executes the shell command via sh -c.
func (e *ShellExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	cmd := exec.CommandContext(ctx, state.ToolPath("sh"), "-c", step.Command)
	return runProcess(ctx, cmd, step.ID, logDir, workdir, env)
}
