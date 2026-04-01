package dag

import "time"

type DAG struct {
	Name   string            `yaml:"name"`
	Env    map[string]string `yaml:"env,omitempty"`
	Params []Param           `yaml:"params,omitempty"`
	Steps  []Step            `yaml:"steps"`
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
}

type Retry struct {
	Limit int `yaml:"limit"`
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
