package executor

import (
	"context"

	"github.com/cynkra/daggle/dag"
)

// ScriptExecutor runs an R script via Rscript.
type ScriptExecutor struct{}

// Run executes the R script via Rscript.
func (e *ScriptExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	return runRScriptSource(ctx, step.Script, step, logDir, workdir, env)
}
