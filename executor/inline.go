package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cynkra/daggle/dag"
)

// InlineRExecutor writes an R expression to a temp file and runs it via Rscript.
type InlineRExecutor struct{}

// Run writes the R expression to a temp file and executes it via Rscript.
func (e *InlineRExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	// Write the R expression to a temp file in the log directory for debuggability
	rCode := wrapErrorOn(step.RExpr, step.ErrorOn)
	tmpFile := filepath.Join(logDir, step.ID+".inline.R")
	if err := os.WriteFile(tmpFile, []byte(rCode), 0644); err != nil {
		return Result{ExitCode: -1, Err: fmt.Errorf("write inline R: %w", err)}
	}

	args := append([]string{"--no-save", "--no-restore", tmpFile}, step.Args...)
	cmd := exec.CommandContext(ctx, "Rscript", args...)
	return runProcess(ctx, cmd, step.ID, logDir, workdir, env)
}
