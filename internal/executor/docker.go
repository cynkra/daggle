package executor

import (
	"context"
	"os/exec"
	"sort"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
)

// DockerExecutor runs a command inside a Docker container. The command is
// supervised like any other subprocess — the outer `docker run` process gets
// the same signal handling, log capture, and timeout behavior as shell steps.
type DockerExecutor struct{}

// Run builds and executes a `docker run --rm ...` invocation.
func (e *DockerExecutor) Run(ctx context.Context, step dag.Step, logDir, workdir string, env []string) Result {
	args := buildDockerArgs(step.Docker)
	cmd := exec.CommandContext(ctx, state.ToolPath("docker"), args...)
	return runProcess(ctx, cmd, step.ID, logDir, workdir, env)
}

// buildDockerArgs constructs the argv for `docker`, honoring each DockerStep
// field. The caller is responsible for supplying a valid step (image and
// command/entrypoint); validation happens in dag.validateStepDocker.
func buildDockerArgs(d *dag.DockerStep) []string {
	args := []string{"run", "--rm"}

	if d.Pull != "" {
		args = append(args, "--pull", d.Pull)
	}
	if d.Network != "" {
		args = append(args, "--network", d.Network)
	}
	if d.User != "" {
		args = append(args, "--user", d.User)
	}
	if d.Workdir != "" {
		args = append(args, "--workdir", d.Workdir)
	}
	if d.Entrypoint != "" {
		args = append(args, "--entrypoint", d.Entrypoint)
	}
	for _, v := range d.Volumes {
		args = append(args, "-v", v)
	}

	// Sort env keys so the generated argv is stable across runs (easier to
	// diff and reason about for cache invalidation).
	keys := make([]string, 0, len(d.Env))
	for k := range d.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+d.Env[k])
	}

	args = append(args, d.Image)

	if d.Command != "" {
		args = append(args, "sh", "-c", d.Command)
	}
	return args
}
