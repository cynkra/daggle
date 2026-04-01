package dag

import "time"

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

type Hook struct {
	RExpr   string `yaml:"r_expr,omitempty"`
	Command string `yaml:"command,omitempty"`
}

type Param struct {
	Name    string `yaml:"name"`
	Default string `yaml:"default,omitempty"`
}

type Step struct {
	ID      string            `yaml:"id"`
	Script  string            `yaml:"script,omitempty"`
	RExpr   string            `yaml:"r_expr,omitempty"`
	Command string            `yaml:"command,omitempty"`
	Args    []string          `yaml:"args,omitempty"`
	Depends []string          `yaml:"depends,omitempty"`
	Timeout string            `yaml:"timeout,omitempty"`
	Retry   *Retry            `yaml:"retry,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
	Workdir string            `yaml:"workdir,omitempty"`

	// Step-level hooks
	OnSuccess *Hook `yaml:"on_success,omitempty"`
	OnFailure *Hook `yaml:"on_failure,omitempty"`
}

type Retry struct {
	Limit int `yaml:"limit"`
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
