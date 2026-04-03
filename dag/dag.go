package dag

import "time"

// DAG represents a directed acyclic graph workflow definition.
type DAG struct {
	Version string            `yaml:"version,omitempty"` // schema version, default "1"
	Name    string            `yaml:"name"`
	Trigger *Trigger          `yaml:"trigger,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
	Params  []Param           `yaml:"params,omitempty"`
	Steps   []Step            `yaml:"steps"`
	Workdir string            `yaml:"workdir,omitempty"`

	// R version constraint, e.g. ">=4.1.0"
	RVersion       string `yaml:"r_version,omitempty"`
	RVersionStrict bool   `yaml:"r_version_strict,omitempty"`

	// SourceDir is the directory containing the DAG YAML file.
	// Set by ParseFile, not from YAML. Used as default working directory.
	SourceDir string `yaml:"-"`

	// Hooks
	OnSuccess *Hook `yaml:"on_success,omitempty"`
	OnFailure *Hook `yaml:"on_failure,omitempty"`
	OnExit    *Hook `yaml:"on_exit,omitempty"`
}

// Trigger defines when a DAG should be automatically executed.
// Multiple trigger types can coexist — any matching trigger starts a run.
type Trigger struct {
	Schedule  string            `yaml:"schedule,omitempty"`
	Watch     *WatchTrigger     `yaml:"watch,omitempty"`
	Webhook   *WebhookTrigger   `yaml:"webhook,omitempty"`
	OnDAG     *OnDAGTrigger     `yaml:"on_dag,omitempty"`
	Condition *ConditionTrigger `yaml:"condition,omitempty"`
	Git       *GitTrigger       `yaml:"git,omitempty"`
	Overlap   string            `yaml:"overlap,omitempty"` // "skip" (default) or "cancel"
}

// WatchTrigger fires when files matching a pattern change in a directory.
type WatchTrigger struct {
	Path     string `yaml:"path"`
	Pattern  string `yaml:"pattern,omitempty"`
	Debounce string `yaml:"debounce,omitempty"` // duration, e.g. "5s"
}

// WebhookTrigger fires on HTTP POST requests.
type WebhookTrigger struct {
	Secret string `yaml:"secret,omitempty"` // HMAC-SHA256 secret
}

// OnDAGTrigger fires when another DAG completes or fails.
type OnDAGTrigger struct {
	Name        string `yaml:"name"`
	Status      string `yaml:"status,omitempty"`       // "completed" (default), "failed", "any"
	PassOutputs bool   `yaml:"pass_outputs,omitempty"`
}

// ConditionTrigger fires when an R expression or shell command succeeds.
type ConditionTrigger struct {
	RExpr        string `yaml:"r_expr,omitempty"`
	Command      string `yaml:"command,omitempty"`
	PollInterval string `yaml:"poll_interval,omitempty"` // duration, default "5m"
}

// GitTrigger fires on new commits or tags in a git repository.
type GitTrigger struct {
	Branch       string   `yaml:"branch,omitempty"`
	Events       []string `yaml:"events,omitempty"`       // "push", "tag"
	PollInterval string   `yaml:"poll_interval,omitempty"` // duration, default "30s"
}

// CronSchedule returns the cron expression from the trigger block, or "" if none.
func (d *DAG) CronSchedule() string {
	if d.Trigger == nil {
		return ""
	}
	return d.Trigger.Schedule
}

// HasTrigger returns true if the DAG has any trigger configured.
func (d *DAG) HasTrigger() bool {
	if d.Trigger == nil {
		return false
	}
	t := d.Trigger
	return t.Schedule != "" || t.Watch != nil || t.Webhook != nil ||
		t.OnDAG != nil || t.Condition != nil || t.Git != nil
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

	// Approval gate
	Approve *ApproveStep `yaml:"approve,omitempty"`

	// Sub-DAG composition
	Call *CallStep `yaml:"call,omitempty"`

	// Additional R step types
	Pin        *PinDeploy     `yaml:"pin,omitempty"`
	Vetiver    *VetiverDeploy `yaml:"vetiver,omitempty"`
	Shinytest  string         `yaml:"shinytest,omitempty"`
	Pkgdown    string         `yaml:"pkgdown,omitempty"`
	Install    string         `yaml:"install,omitempty"`
	Targets    string         `yaml:"targets,omitempty"`
	Benchmark  string         `yaml:"benchmark,omitempty"`
	Revdepcheck string        `yaml:"revdepcheck,omitempty"`

	// Posit Connect deployment
	Connect *ConnectDeploy `yaml:"connect,omitempty"`

	// Matrix: expand step into parameter grid
	Matrix      map[string][]string `yaml:"matrix,omitempty"`
	MaxParallel int                 `yaml:"max_parallel,omitempty"`

	// Conditional execution: skip step if condition is false
	When *StepCondition `yaml:"when,omitempty"`

	// Preconditions: health checks before running the step
	Preconditions []Precondition `yaml:"preconditions,omitempty"`

	// Error sensitivity: "error" (default), "warning", "message"
	ErrorOn string `yaml:"error_on,omitempty"`

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

// ApproveStep pauses execution until a human approves or rejects.
type ApproveStep struct {
	Message string `yaml:"message,omitempty"`
	Timeout string `yaml:"timeout,omitempty"` // e.g. "24h", default: no timeout
	Notify  *Hook  `yaml:"notify,omitempty"`
}

// CallStep configures sub-DAG composition.
type CallStep struct {
	DAG    string            `yaml:"dag"`              // name of another DAG to execute
	Params map[string]string `yaml:"params,omitempty"` // parameter overrides for the sub-DAG
}

// StepCondition defines when a step should run.
type StepCondition struct {
	RExpr   string `yaml:"r_expr,omitempty"`
	Command string `yaml:"command,omitempty"`
}

// Precondition defines a health check that must pass before a step runs.
type Precondition struct {
	RExpr   string `yaml:"r_expr,omitempty"`
	Command string `yaml:"command,omitempty"`
}

// PinDeploy configures publishing data/models via the pins package.
type PinDeploy struct {
	Board     string `yaml:"board"`               // connect, s3, local, azure
	Name      string `yaml:"name"`                // pin name
	Object    string `yaml:"object"`              // path to object file
	Type      string `yaml:"type,omitempty"`      // rds, csv, parquet, arrow, json
	Versioned *bool  `yaml:"versioned,omitempty"` // default true
}

// VetiverDeploy configures MLOps model versioning/deployment.
type VetiverDeploy struct {
	Action string `yaml:"action"`          // pin or deploy
	Model  string `yaml:"model,omitempty"` // path to model file (for pin action)
	Board  string `yaml:"board,omitempty"` // connect, s3, local
	Name   string `yaml:"name"`            // model name
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
	case s.Approve != nil:
		return "approve"
	case s.Call != nil:
		return "call"
	case s.Pin != nil:
		return "pin"
	case s.Vetiver != nil:
		return "vetiver"
	case s.Shinytest != "":
		return "shinytest"
	case s.Pkgdown != "":
		return "pkgdown"
	case s.Install != "":
		return "install"
	case s.Targets != "":
		return "targets"
	case s.Benchmark != "":
		return "benchmark"
	case s.Revdepcheck != "":
		return "revdepcheck"
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
