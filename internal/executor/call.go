package executor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cynkra/daggle/dag"
)

// CallExecutor executes another DAG as a sub-DAG by invoking daggle recursively.
type CallExecutor struct{}

// Run invokes daggle run for the referenced sub-DAG.
func (e *CallExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	c := step.Call

	args := []string{"run", c.DAG}
	for k, v := range c.Params {
		args = append(args, "-p", k+"="+v)
	}

	// Find the daggle binary (ourselves)
	self, err := os.Executable()
	if err != nil {
		self = "daggle"
	}

	// Set dags dir so the sub-DAG is found relative to the parent
	dagsDirEnv := ""
	if workdir != "" {
		// Check for .daggle/ in workdir
		localDir := filepath.Join(workdir, ".daggle")
		if info, err := os.Stat(localDir); err == nil && info.IsDir() {
			dagsDirEnv = "DAGGLE_DAGS_DIR=" + localDir
		}
	}

	subEnv := make([]string, len(env))
	copy(subEnv, env)
	if dagsDirEnv != "" {
		subEnv = append(subEnv, dagsDirEnv)
	}

	cmd := exec.CommandContext(ctx, self, args...)
	return runProcess(ctx, cmd, step.ID, logDir, workdir, subEnv)
}
