package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
)

// runRScript writes R code to a temp file and executes it via Rscript.
// The suffix is used to name the temp file (e.g. "inline", "rpkg", "rmd").
func runRScript(ctx context.Context, rCode string, step dag.Step, logDir, workdir string, env []string, suffix string) Result {
	tmpFile := filepath.Join(logDir, step.ID+"."+suffix+".R")
	if err := os.WriteFile(tmpFile, []byte(rCode), 0644); err != nil {
		return Result{ExitCode: -1, Err: fmt.Errorf("write %s R: %w", suffix, err)}
	}
	args := append([]string{"--no-save", "--no-restore", tmpFile}, step.Args...)
	cmd := exec.CommandContext(ctx, state.ToolPath("rscript"), args...)
	return runProcess(ctx, cmd, step.ID, logDir, workdir, env)
}
