package executor

import (
	"context"
	"time"

	"github.com/cynkra/daggle/dag"
)

// Result holds the outcome of executing a step.
type Result struct {
	ExitCode    int
	Stdout      string // path to stdout log file
	Stderr      string // path to stderr log file
	Duration    time.Duration
	Err         error
	Outputs     map[string]string // parsed ::daggle-output:: values
	ErrorDetail string            // extracted R error message from stderr
}

// Executor runs a single DAG step.
type Executor interface {
	Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result
}

// New returns the appropriate executor for the given step type.
func New(step dag.Step) Executor {
	typ := dag.StepType(step)
	switch typ {
	case "script":
		return &ScriptExecutor{}
	case "r_expr":
		return &InlineRExecutor{}
	case "command":
		return &ShellExecutor{}
	case "quarto":
		return &QuartoExecutor{}
	case "test", "check", "document", "lint", "style":
		return &RPkgExecutor{Action: typ}
	case "connect":
		return &ConnectExecutor{}
	default:
		return nil
	}
}
