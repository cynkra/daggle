package executor

import (
	"context"
	"os/exec"

	"github.com/cynkra/daggle/dag"
)

// QuartoExecutor renders Quarto documents or projects.
type QuartoExecutor struct{}

// Run executes quarto render with the specified path and arguments.
func (e *QuartoExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	args := append([]string{"render", step.Quarto}, step.Args...)
	cmd := exec.CommandContext(ctx, "quarto", args...)
	return runProcess(ctx, cmd, step.ID, logDir, workdir, env)
}
