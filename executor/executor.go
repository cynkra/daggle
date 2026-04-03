package executor

import (
	"context"
	"fmt"
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

// wrapErrorOn wraps R code in withCallingHandlers if the step has error_on set
// to "warning" or "message". This causes R warnings or messages to be promoted to errors.
func wrapErrorOn(rCode string, errorOn string) string {
	if errorOn == "" || errorOn == "error" {
		return rCode
	}

	var handler string
	switch errorOn {
	case "warning":
		handler = `warning = function(w) {
      message(paste("Caught warning:", conditionMessage(w)))
      stop(paste("Step failed due to warning:", conditionMessage(w)), call. = FALSE)
    }`
	case "message":
		handler = `warning = function(w) {
      message(paste("Caught warning:", conditionMessage(w)))
      stop(paste("Step failed due to warning:", conditionMessage(w)), call. = FALSE)
    },
    message = function(m) {
      cat(conditionMessage(m))
      stop(paste("Step failed due to message:", conditionMessage(m)), call. = FALSE)
    }`
	default:
		return rCode
	}

	return fmt.Sprintf("withCallingHandlers({\n%s\n}, %s)\n", rCode, handler)
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
	case "test", "check", "document", "lint", "style", "renv_restore", "coverage",
		"shinytest", "pkgdown", "install", "targets", "benchmark", "revdepcheck":
		return &RPkgExecutor{Action: typ}
	case "rmd":
		return &RmdExecutor{}
	case "validate":
		return &ValidateExecutor{}
	case "call":
		return &CallExecutor{}
	case "pin":
		return &PinExecutor{}
	case "vetiver":
		return &VetiverExecutor{}
	case "connect":
		return &ConnectExecutor{}
	default:
		return nil
	}
}
