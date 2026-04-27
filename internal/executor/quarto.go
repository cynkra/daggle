package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
)

// QuartoExecutor renders Quarto documents or projects.
type QuartoExecutor struct{}

// Run executes quarto render with the specified path and arguments.
func (e *QuartoExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	args := []string{"render", step.Quarto}

	if step.OutputDir != "" {
		resolved, err := resolveQuartoOutputDir(step.OutputDir, workdir)
		if err != nil {
			return Result{Err: err}
		}
		if err := os.MkdirAll(resolved, 0o755); err != nil {
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

// resolveQuartoOutputDir validates that an output_dir resolves under workdir.
// Without this check, a templated `output_dir: "{{ .Param.dir }}"` lets an
// API caller escape the workdir by passing `../../somewhere`. Returns the
// resolved absolute path on success, or an error if the path escapes workdir
// or workdir is empty (which would mean we have no anchor to validate against).
func resolveQuartoOutputDir(outputDir, workdir string) (string, error) {
	if filepath.IsAbs(outputDir) {
		// An absolute path was set explicitly by the DAG author (not via a
		// param); honour it. Templating into an absolute path requires the
		// DAG author to write `output_dir: /tmp/{{ ... }}`, which is
		// indistinguishable from intent.
		return filepath.Clean(outputDir), nil
	}
	if workdir == "" {
		return "", fmt.Errorf("quarto: output_dir %q is relative but workdir is empty", outputDir)
	}
	cleanWork := filepath.Clean(workdir) + string(filepath.Separator)
	resolved := filepath.Clean(filepath.Join(workdir, outputDir))
	if !strings.HasPrefix(resolved+string(filepath.Separator), cleanWork) && resolved != filepath.Clean(workdir) {
		return "", fmt.Errorf("quarto: output_dir %q escapes workdir %q", outputDir, workdir)
	}
	return resolved, nil
}
