package dag

import "time"

// DAG represents a directed acyclic graph workflow definition.
type DAG struct {
	Name     string            `yaml:"name"`
	Schedule string            `yaml:"schedule,omitempty"`
	Env      map[string]string `yaml:"env,omitempty"`
	Params   []Param           `yaml:"params,omitempty"`
	Steps    []Step            `yaml:"steps"`
	Workdir  string            `yaml:"workdir,omitempty"`

	// SourceDir is the directory containing the DAG YAML file.
	// Set by ParseFile, not from YAML. Used as default working directory.
	SourceDir string `yaml:"-"`

	// Hooks
	OnSuccess *Hook `yaml:"on_success,omitempty"`
	OnFailure *Hook `yaml:"on_failure,omitempty"`
	OnExit    *Hook `yaml:"on_exit,omitempty"`
}

// Hook defines a lifecycle action triggered on success, failure, or exit.
type Hook struct {
	RExpr   string `yaml:"r_expr,omitempty"`
	Command string `yaml:"command,omitempty"`
}

// Param defines a named parameter with an optional default value.
type Param struct {
	Name    string `yaml:"name"`
	Default string `yaml:"default,omitempty"`
}

// Step defines a single unit of work within a DAG.
type Step struct {
	ID      string            `yaml:"id"`
	Script  string            `yaml:"script,omitempty"`
	RExpr   string            `yaml:"r_expr,omitempty"`
	Command string            `yaml:"command,omitempty"`
	Quarto  string            `yaml:"quarto,omitempty"`
	Args    []string          `yaml:"args,omitempty"`
	Depends []string          `yaml:"depends,omitempty"`
	Timeout string            `yaml:"timeout,omitempty"`
	Retry   *Retry            `yaml:"retry,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
	Workdir string            `yaml:"workdir,omitempty"`

	// R package development step types
	Test     string `yaml:"test,omitempty"`
	Check    string `yaml:"check,omitempty"`
	Document string `yaml:"document,omitempty"`
	Lint     string `yaml:"lint,omitempty"`
	Style    string `yaml:"style,omitempty"`

	// R workflow step types
	Rmd         string `yaml:"rmd,omitempty"`
	RenvRestore string `yaml:"renv_restore,omitempty"`
	Coverage    string `yaml:"coverage,omitempty"`
	Validate    string `yaml:"validate,omitempty"`

	// Posit Connect deployment
	Connect *ConnectDeploy `yaml:"connect,omitempty"`

	// Step-level hooks
	OnSuccess *Hook `yaml:"on_success,omitempty"`
	OnFailure *Hook `yaml:"on_failure,omitempty"`
}

// ConnectDeploy configures deployment to Posit Connect.
type ConnectDeploy struct {
	Type        string `yaml:"type"`                  // shiny, quarto, plumber
	Path        string `yaml:"path"`                  // content directory or file
	Name        string `yaml:"name,omitempty"`         // content name on Connect
	ForceUpdate *bool  `yaml:"force_update,omitempty"` // default true
}

// Retry configures retry behavior for a step.
type Retry struct {
	Limit    int    `yaml:"limit"`
	Backoff  string `yaml:"backoff,omitempty"`   // "linear" (default) or "exponential"
	MaxDelay string `yaml:"max_delay,omitempty"` // cap on delay, e.g. "60s"
}

// ResolveWorkdir returns the effective working directory for a step.
// Precedence: step workdir > DAG workdir > DAG source directory.
func (d *DAG) ResolveWorkdir(s Step) string {
	if s.Workdir != "" {
		return s.Workdir
	}
	if d.Workdir != "" {
		return d.Workdir
	}
	if d.SourceDir != "" {
		return d.SourceDir
	}
	return ""
}

// StepType returns the type of the step based on which field is set.
func StepType(s Step) string {
	switch {
	case s.Script != "":
		return "script"
	case s.RExpr != "":
		return "r_expr"
	case s.Command != "":
		return "command"
	case s.Quarto != "":
		return "quarto"
	case s.Test != "":
		return "test"
	case s.Check != "":
		return "check"
	case s.Document != "":
		return "document"
	case s.Lint != "":
		return "lint"
	case s.Style != "":
		return "style"
	case s.Rmd != "":
		return "rmd"
	case s.RenvRestore != "":
		return "renv_restore"
	case s.Coverage != "":
		return "coverage"
	case s.Validate != "":
		return "validate"
	case s.Connect != nil:
		return "connect"
	default:
		return ""
	}
}

// ParseTimeout parses the step's timeout string into a time.Duration.
// Returns 0 if no timeout is set.
func (s Step) ParseTimeout() (time.Duration, error) {
	if s.Timeout == "" {
		return 0, nil
	}
	return time.ParseDuration(s.Timeout)
}

// MaxAttempts returns the maximum number of execution attempts for a step.
func (s Step) MaxAttempts() int {
	if s.Retry != nil && s.Retry.Limit > 0 {
		return s.Retry.Limit + 1
	}
	return 1
}
