package executor

import (
	"context"
	"time"

	"github.com/schochastics/rdag/dag"
)

// Result holds the outcome of executing a step.
type Result struct {
	ExitCode int
	Stdout   string // path to stdout log file
	Stderr   string // path to stderr log file
	Duration time.Duration
	Err      error
}

// Executor runs a single DAG step.
type Executor interface {
	Run(ctx context.Context, step dag.Step, logDir string, env []string) Result
}

// New returns the appropriate executor for the given step type.
func New(step dag.Step) Executor {
	switch dag.StepType(step) {
	case "script":
		return &ScriptExecutor{}
	case "r_expr":
		return &InlineRExecutor{}
	case "command":
		return &ShellExecutor{}
	default:
		return nil
	}
}
