package executor

import (
	"context"

	"github.com/cynkra/daggle/dag"
)

// InlineRExecutor writes an R expression to a temp file and runs it via Rscript.
type InlineRExecutor struct{}

// Run writes the R expression to a temp file and executes it via Rscript.
func (e *InlineRExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	rCode := wrapErrorOn(step.RExpr, step.ErrorOn)
	return runRScript(ctx, rCode, step, logDir, workdir, env, "inline")
}
