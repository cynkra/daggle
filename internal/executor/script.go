package executor

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
)

// ScriptExecutor runs an R script via Rscript.
type ScriptExecutor struct{}

// Run executes the R script via Rscript.
func (e *ScriptExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	if step.ErrorOn != "" && step.ErrorOn != "error" {
		rCode := wrapErrorOn(fmt.Sprintf("source(%q)", step.Script), step.ErrorOn)
		return runRScript(ctx, rCode, step, logDir, workdir, env, "wrapper")
	}
	args := append([]string{"--no-save", "--no-restore", step.Script}, step.Args...)
	cmd := exec.CommandContext(ctx, state.ToolPath("rscript"), args...)
	return runProcess(ctx, cmd, step.ID, logDir, workdir, env)
}
