package executor

import (
	"context"
	"os/exec"

	"github.com/schochastics/rdag/dag"
)

// ScriptExecutor runs an R script via Rscript.
type ScriptExecutor struct{}

func (e *ScriptExecutor) Run(ctx context.Context, step dag.Step, logDir string, env []string) Result {
	args := append([]string{"--no-save", "--no-restore", step.Script}, step.Args...)
	cmd := exec.CommandContext(ctx, "Rscript", args...)
	return runProcess(ctx, cmd, step.ID, logDir, env)
}
