package executor

import (
	"context"
	"fmt"
	"time"

	"github.com/cynkra/daggle/dag"
)

// Summary holds a step summary emitted via ::daggle-summary:: markers.
type Summary struct {
	Format  string // "markdown"
	Content string
}

// MetaEntry holds a metadata entry emitted via ::daggle-meta:: markers.
type MetaEntry struct {
	Name  string
	Type  string // "numeric", "text", "table", "image"
	Value string
}

// ValidationResult holds a validation result emitted via ::daggle-validation:: markers.
type ValidationResult struct {
	Name    string
	Status  string // "pass", "warn", "fail"
	Message string
}

// Result holds the outcome of executing a step.
type Result struct {
	ExitCode    int
	Stdout      string // path to stdout log file
	Stderr      string // path to stderr log file
	Duration    time.Duration
	Err         error
	Outputs     map[string]string  // parsed ::daggle-output:: values
	ErrorDetail string             // extracted error message from stderr (R, Quarto, or last lines)
	Summaries   []Summary          // parsed ::daggle-summary:: values
	Metadata    []MetaEntry        // parsed ::daggle-meta:: values
	Validations []ValidationResult // parsed ::daggle-validation:: values

	// Resource usage captured from syscall.Rusage after cmd.Wait().
	// Zero when unavailable (timeout, start failure, unsupported platform).
	PeakRSSKB  int64   // peak resident set size in KB (normalized across platforms)
	UserCPUSec float64 // user CPU time in seconds
	SysCPUSec  float64 // system CPU time in seconds
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
	case "approve":
		return &ApproveExecutor{}
	case "call":
		return &CallExecutor{}
	case "pin":
		return &PinExecutor{}
	case "vetiver":
		return &VetiverExecutor{}
	case "connect":
		return &ConnectExecutor{}
	case "database":
		return &DatabaseExecutor{}
	case "email":
		return &EmailExecutor{}
	default:
		return nil
	}
}
