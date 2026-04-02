package executor

import (
	"context"
	"os/exec"

	"github.com/cynkra/daggle/dag"
)

// ValidateExecutor runs a data validation R script via Rscript.
// Functionally identical to ScriptExecutor but provides a distinct step type
// for status output and editor autocomplete.
type ValidateExecutor struct{}

// Run executes the validation script via Rscript.
func (e *ValidateExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	args := append([]string{"--no-save", "--no-restore", step.Validate}, step.Args...)
	cmd := exec.CommandContext(ctx, "Rscript", args...)
	return runProcess(ctx, cmd, step.ID, logDir, workdir, env)
}
