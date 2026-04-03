package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cynkra/daggle/dag"
)

// ScriptExecutor runs an R script via Rscript.
type ScriptExecutor struct{}

// Run executes the R script via Rscript.
func (e *ScriptExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	if step.ErrorOn != "" && step.ErrorOn != "error" {
		// Generate a wrapper script that sources the original inside withCallingHandlers
		rCode := fmt.Sprintf("source(%q)", step.Script)
		rCode = wrapErrorOn(rCode, step.ErrorOn)
		wrapperFile := filepath.Join(logDir, step.ID+".wrapper.R")
		if err := os.WriteFile(wrapperFile, []byte(rCode), 0644); err != nil {
			return Result{ExitCode: -1, Err: fmt.Errorf("write script wrapper: %w", err)}
		}
		args := append([]string{"--no-save", "--no-restore", wrapperFile}, step.Args...)
		cmd := exec.CommandContext(ctx, "Rscript", args...)
		return runProcess(ctx, cmd, step.ID, logDir, workdir, env)
	}
	args := append([]string{"--no-save", "--no-restore", step.Script}, step.Args...)
	cmd := exec.CommandContext(ctx, "Rscript", args...)
	return runProcess(ctx, cmd, step.ID, logDir, workdir, env)
}
