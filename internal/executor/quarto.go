package executor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
)

// QuartoExecutor renders Quarto documents or projects.
type QuartoExecutor struct{}

// Run executes quarto render with the specified path and arguments.
func (e *QuartoExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	args := []string{"render", step.Quarto}

	if step.OutputDir != "" {
		dir := step.OutputDir
		if !filepath.IsAbs(dir) && workdir != "" {
			dir = filepath.Join(workdir, dir)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Result{Err: err}
		}
		args = append(args, "--output-dir", step.OutputDir)
	}
	if step.OutputName != "" {
		args = append(args, "--output", step.OutputName)
	}

	args = append(args, step.Args...)
	cmd := exec.CommandContext(ctx, state.ToolPath("quarto"), args...)
	return runProcess(ctx, cmd, step.ID, logDir, workdir, env)
}
