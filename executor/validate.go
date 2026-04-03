package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cynkra/daggle/dag"
)

// ValidateExecutor runs a data validation R script via Rscript.
// Functionally identical to ScriptExecutor but provides a distinct step type
// for status output and editor autocomplete.
type ValidateExecutor struct{}

// Run executes the validation script via Rscript.
func (e *ValidateExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	if step.ErrorOn != "" && step.ErrorOn != "error" {
		rCode := wrapErrorOn(fmt.Sprintf("source(%q)", step.Validate), step.ErrorOn)
		wrapperFile := filepath.Join(logDir, step.ID+".wrapper.R")
		if err := os.WriteFile(wrapperFile, []byte(rCode), 0644); err != nil {
			return Result{ExitCode: -1, Err: fmt.Errorf("write validate wrapper: %w", err)}
		}
		args := append([]string{"--no-save", "--no-restore", wrapperFile}, step.Args...)
		cmd := exec.CommandContext(ctx, "Rscript", args...)
		return runProcess(ctx, cmd, step.ID, logDir, workdir, env)
	}
	args := append([]string{"--no-save", "--no-restore", step.Validate}, step.Args...)
	cmd := exec.CommandContext(ctx, "Rscript", args...)
	return runProcess(ctx, cmd, step.ID, logDir, workdir, env)
}
