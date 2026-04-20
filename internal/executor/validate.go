package executor

import (
	"context"

	"github.com/cynkra/daggle/dag"
)

// ValidateExecutor runs a data validation R script via Rscript.
// Same execution as ScriptExecutor — kept as a distinct type so DAG
// validation, status output, and editor autocomplete can distinguish
// the two step kinds.
type ValidateExecutor struct{}

// Run executes the validation script via Rscript.
func (e *ValidateExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	return runRScriptSource(ctx, step.Validate, step, logDir, workdir, env)
}
