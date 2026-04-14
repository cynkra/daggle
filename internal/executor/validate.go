package executor

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
)

// ValidateExecutor runs a data validation R script via Rscript.
// Functionally identical to ScriptExecutor but provides a distinct step type
// for status output and editor autocomplete.
type ValidateExecutor struct{}

// Run executes the validation script via Rscript.
func (e *ValidateExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	if step.ErrorOn != "" && step.ErrorOn != "error" {
		rCode := wrapErrorOn(fmt.Sprintf("source(%q)", step.Validate), step.ErrorOn)
		return runRScript(ctx, rCode, step, logDir, workdir, env, "wrapper")
	}
	args := append([]string{"--no-save", "--no-restore", step.Validate}, step.Args...)
	cmd := exec.CommandContext(ctx, state.ToolPath("rscript"), args...)
	return runProcess(ctx, cmd, step.ID, logDir, workdir, env)
}
