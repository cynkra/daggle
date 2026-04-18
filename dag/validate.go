package dag

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// artifactNameRe validates artifact names: alphanumeric + underscores, starting with letter or underscore.
var artifactNameRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// deadlineRe validates HH:MM time format.
var deadlineRe = regexp.MustCompile(`^\d{2}:\d{2}$`)

// Validate checks that a DAG definition is well-formed.
func Validate(d *DAG) error {
	var errs []string

	if d.Name == "" {
		errs = append(errs, "dag name is required")
	}

	if len(d.Steps) == 0 {
		errs = append(errs, "dag must have at least one step")
	}

	errs = append(errs, validateSteps(d)...)
	errs = append(errs, validateRVersion(d)...)
	errs = append(errs, validateTriggers(d)...)
	errs = append(errs, validateHooks(d)...)
	errs = append(errs, validateExposures(d)...)
	errs = append(errs, validateCycles(d, errs)...)

	if len(errs) > 0 {
		return fmt.Errorf("validation errors:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// validateSteps validates all step definitions including IDs, types, dependencies,
// timeouts, error_on, artifacts, freshness, cache compatibility, and retry config.
func validateSteps(d *DAG) []string {
	var errs []string

	// Build complete ID set for dependency validation
	allIDs := make(map[string]bool, len(d.Steps))
	for _, s := range d.Steps {
		if s.ID != "" {
			allIDs[s.ID] = true
		}
	}

	ids := make(map[string]bool, len(d.Steps))
	for _, s := range d.Steps {
		if s.ID == "" {
			errs = append(errs, "step id is required")
			continue
		}
		if ids[s.ID] {
			errs = append(errs, fmt.Sprintf("duplicate step id: %q", s.ID))
		}
		ids[s.ID] = true

		errs = append(errs, validateStepType(s)...)
		errs = append(errs, validateStepConnect(s)...)
		errs = append(errs, validateStepDependencies(s, allIDs)...)
		errs = append(errs, validateStepTimeout(s)...)
		errs = append(errs, validateStepErrorOn(s)...)
		errs = append(errs, validateStepArtifacts(s)...)
		errs = append(errs, validateStepFreshness(s)...)
		errs = append(errs, validateStepCache(s)...)
		errs = append(errs, validateStepRetry(s)...)
		errs = append(errs, validateStepApprove(s)...)
	}
	return errs
}

func validateStepType(s Step) []string {
	typeCount := 0
	for _, set := range []bool{
		s.Script != "", s.RExpr != "", s.Command != "", s.Quarto != "",
		s.Test != "", s.Check != "", s.Document != "", s.Lint != "", s.Style != "",
		s.Rmd != "", s.RenvRestore != "", s.Coverage != "", s.Validate != "",
		s.Approve != nil, s.Call != nil, s.Pin != nil, s.Vetiver != nil,
		s.Shinytest != "", s.Pkgdown != "", s.Install != "", s.Targets != "",
		s.Benchmark != "", s.Revdepcheck != "",
		s.Connect != nil,
	} {
		if set {
			typeCount++
		}
	}
	stepTypes := "script, r_expr, command, quarto, test, check, document, lint, style, rmd, renv_restore, coverage, validate, approve, call, pin, vetiver, shinytest, pkgdown, install, targets, benchmark, revdepcheck, connect"
	var errs []string
	if typeCount == 0 {
		errs = append(errs, fmt.Sprintf("step %q must have one of: %s", s.ID, stepTypes))
	}
	if typeCount > 1 {
		errs = append(errs, fmt.Sprintf("step %q has multiple types; use exactly one of: %s", s.ID, stepTypes))
	}
	return errs
}

func validateStepConnect(s Step) []string {
	if s.Connect == nil {
		return nil
	}
	var errs []string
	validTypes := map[string]bool{"shiny": true, "quarto": true, "plumber": true}
	if s.Connect.Type == "" {
		errs = append(errs, fmt.Sprintf("step %q connect.type is required (shiny, quarto, plumber)", s.ID))
	} else if !validTypes[s.Connect.Type] {
		errs = append(errs, fmt.Sprintf("step %q connect.type %q is invalid; must be one of: shiny, quarto, plumber", s.ID, s.Connect.Type))
	}
	if s.Connect.Path == "" {
		errs = append(errs, fmt.Sprintf("step %q connect.path is required", s.ID))
	}
	return errs
}

func validateStepDependencies(s Step, allIDs map[string]bool) []string {
	var errs []string
	for _, dep := range s.Depends {
		if !allIDs[dep] {
			errs = append(errs, fmt.Sprintf("step %q depends on unknown step %q", s.ID, dep))
		}
	}
	return errs
}

func validateStepTimeout(s Step) []string {
	if s.Timeout == "" {
		return nil
	}
	if _, err := s.ParseTimeout(); err != nil {
		return []string{fmt.Sprintf("step %q has invalid timeout %q: %v", s.ID, s.Timeout, err)}
	}
	return nil
}

// validateStepApprove rejects invalid durations in approve.timeout. An empty
// string is valid and means "no timeout" — matches the runtime behavior in
// internal/executor/approve.go.
func validateStepApprove(s Step) []string {
	if s.Approve == nil || s.Approve.Timeout == "" {
		return nil
	}
	if _, err := time.ParseDuration(s.Approve.Timeout); err != nil {
		return []string{fmt.Sprintf("step %q approve.timeout %q is invalid: %v", s.ID, s.Approve.Timeout, err)}
	}
	return nil
}

func validateStepErrorOn(s Step) []string {
	if s.ErrorOn == "" {
		return nil
	}
	validErrorOn := map[string]bool{"error": true, "warning": true, "message": true}
	if !validErrorOn[s.ErrorOn] {
		return []string{fmt.Sprintf("step %q error_on %q is invalid; must be one of: error, warning, message", s.ID, s.ErrorOn)}
	}
	return nil
}

func validateStepArtifacts(s Step) []string {
	if len(s.Artifacts) == 0 {
		return nil
	}
	var errs []string
	artNames := make(map[string]bool)
	for _, art := range s.Artifacts {
		switch {
		case art.Name == "":
			errs = append(errs, fmt.Sprintf("step %q artifact name is required", s.ID))
		case !artifactNameRe.MatchString(art.Name):
			errs = append(errs, fmt.Sprintf("step %q artifact name %q must match [a-zA-Z_][a-zA-Z0-9_]*", s.ID, art.Name))
		case artNames[art.Name]:
			errs = append(errs, fmt.Sprintf("step %q has duplicate artifact name %q", s.ID, art.Name))
		}
		artNames[art.Name] = true
		if art.Path == "" {
			errs = append(errs, fmt.Sprintf("step %q artifact %q path is required", s.ID, art.Name))
		} else if filepath.IsAbs(art.Path) {
			errs = append(errs, fmt.Sprintf("step %q artifact %q path must be relative, got %q", s.ID, art.Name, art.Path))
		}
	}
	return errs
}

func validateStepFreshness(s Step) []string {
	var errs []string
	for i, fc := range s.Freshness {
		if fc.Path == "" {
			errs = append(errs, fmt.Sprintf("step %q freshness[%d].path is required", s.ID, i))
		}
		if fc.MaxAge == "" {
			errs = append(errs, fmt.Sprintf("step %q freshness[%d].max_age is required", s.ID, i))
		} else if _, err := time.ParseDuration(fc.MaxAge); err != nil {
			errs = append(errs, fmt.Sprintf("step %q freshness[%d].max_age %q is invalid: %v", s.ID, i, fc.MaxAge, err))
		}
		switch fc.OnStale {
		case "", "fail", "warn":
			// valid
		default:
			errs = append(errs, fmt.Sprintf("step %q freshness[%d].on_stale %q is invalid; must be one of: fail, warn", s.ID, i, fc.OnStale))
		}
	}
	return errs
}

func validateStepCache(s Step) []string {
	if !s.Cache {
		return nil
	}
	var errs []string
	if s.Approve != nil {
		errs = append(errs, fmt.Sprintf("step %q: cache is incompatible with approve steps", s.ID))
	}
	if s.Call != nil {
		errs = append(errs, fmt.Sprintf("step %q: cache is incompatible with call steps", s.ID))
	}
	return errs
}

func validateStepRetry(s Step) []string {
	if s.Retry == nil {
		return nil
	}
	var errs []string
	if s.Retry.Limit < 0 {
		errs = append(errs, fmt.Sprintf("step %q retry limit must be >= 0", s.ID))
	}
	if s.Retry.Backoff != "" && s.Retry.Backoff != "linear" && s.Retry.Backoff != "exponential" {
		errs = append(errs, fmt.Sprintf("step %q retry backoff must be \"linear\" or \"exponential\"", s.ID))
	}
	if s.Retry.MaxDelay != "" {
		if _, err := time.ParseDuration(s.Retry.MaxDelay); err != nil {
			errs = append(errs, fmt.Sprintf("step %q retry max_delay %q is invalid: %v", s.ID, s.Retry.MaxDelay, err))
		}
	}
	return errs
}

// validateRVersion validates the r_version constraint if present.
func validateRVersion(d *DAG) []string {
	if d.RVersion == "" {
		return nil
	}
	if err := validateRVersionConstraint(d.RVersion); err != nil {
		return []string{fmt.Sprintf("r_version %q is invalid: %v", d.RVersion, err)}
	}
	return nil
}

// validateTriggers validates the trigger block if present.
func validateTriggers(d *DAG) []string {
	if d.Trigger == nil {
		return nil
	}
	var errs []string
	t := d.Trigger

	if t.Overlap != "" && t.Overlap != "skip" && t.Overlap != "cancel" {
		errs = append(errs, fmt.Sprintf("trigger.overlap %q is invalid; must be one of: skip, cancel", t.Overlap))
	}
	if t.Watch != nil {
		if t.Watch.Path == "" {
			errs = append(errs, "trigger.watch.path is required")
		}
		if t.Watch.Debounce != "" {
			if _, err := time.ParseDuration(t.Watch.Debounce); err != nil {
				errs = append(errs, fmt.Sprintf("trigger.watch.debounce %q is invalid: %v", t.Watch.Debounce, err))
			}
		}
	}
	if t.OnDAG != nil {
		if t.OnDAG.Name == "" {
			errs = append(errs, "trigger.on_dag.name is required")
		}
		validStatuses := map[string]bool{"": true, "completed": true, "failed": true, "any": true}
		if !validStatuses[t.OnDAG.Status] {
			errs = append(errs, fmt.Sprintf("trigger.on_dag.status %q is invalid; must be one of: completed, failed, any", t.OnDAG.Status))
		}
	}
	if t.Condition != nil {
		if t.Condition.RExpr == "" && t.Condition.Command == "" {
			errs = append(errs, "trigger.condition requires r_expr or command")
		}
		if t.Condition.PollInterval != "" {
			if _, err := time.ParseDuration(t.Condition.PollInterval); err != nil {
				errs = append(errs, fmt.Sprintf("trigger.condition.poll_interval %q is invalid: %v", t.Condition.PollInterval, err))
			}
		}
	}
	if t.Git != nil {
		if t.Git.PollInterval != "" {
			if _, err := time.ParseDuration(t.Git.PollInterval); err != nil {
				errs = append(errs, fmt.Sprintf("trigger.git.poll_interval %q is invalid: %v", t.Git.PollInterval, err))
			}
		}
		validEvents := map[string]bool{"push": true, "tag": true}
		for _, ev := range t.Git.Events {
			if !validEvents[ev] {
				errs = append(errs, fmt.Sprintf("trigger.git.events contains invalid event %q; must be one of: push, tag", ev))
			}
		}
	}
	if t.Deadline != "" {
		if !deadlineRe.MatchString(t.Deadline) {
			errs = append(errs, fmt.Sprintf("trigger.deadline %q is invalid; must be HH:MM format", t.Deadline))
		} else {
			if err := validateDeadlineTime(t.Deadline); err != nil {
				errs = append(errs, fmt.Sprintf("trigger.deadline %q is invalid: %v", t.Deadline, err))
			}
		}
	}
	if t.OnDeadline != nil && t.Deadline == "" {
		errs = append(errs, "trigger.on_deadline requires trigger.deadline to be set")
	}
	return errs
}

// validateHooks checks that DAG-level hooks use exactly one of r_expr, command, or notify.
func validateHooks(d *DAG) []string {
	var errs []string
	check := func(h *Hook, where string) {
		if h == nil {
			return
		}
		count := 0
		if h.RExpr != "" {
			count++
		}
		if h.Command != "" {
			count++
		}
		if h.Notify != "" {
			count++
		}
		if count == 0 {
			errs = append(errs, fmt.Sprintf("%s: must set one of r_expr, command, or notify", where))
		}
		if count > 1 {
			errs = append(errs, fmt.Sprintf("%s: must set exactly one of r_expr, command, or notify", where))
		}
	}
	check(d.OnSuccess, "on_success")
	check(d.OnFailure, "on_failure")
	check(d.OnExit, "on_exit")
	if d.Trigger != nil {
		check(d.Trigger.OnDeadline, "trigger.on_deadline")
	}
	return errs
}

// validateExposures checks exposures[].name is unique and non-empty,
// and type is one of the accepted values.
func validateExposures(d *DAG) []string {
	if len(d.Exposures) == 0 {
		return nil
	}
	var errs []string
	validTypes := map[string]bool{
		"shiny":     true,
		"quarto":    true,
		"dashboard": true,
		"report":    true,
		"other":     true,
	}
	seen := make(map[string]bool, len(d.Exposures))
	for i, ex := range d.Exposures {
		if ex.Name == "" {
			errs = append(errs, fmt.Sprintf("exposures[%d].name is required", i))
			continue
		}
		if seen[ex.Name] {
			errs = append(errs, fmt.Sprintf("exposures[%d]: duplicate name %q", i, ex.Name))
		}
		seen[ex.Name] = true
		if ex.Type == "" {
			errs = append(errs, fmt.Sprintf("exposures[%d] (%s): type is required", i, ex.Name))
		} else if !validTypes[ex.Type] {
			errs = append(errs, fmt.Sprintf("exposures[%d] (%s): type %q is invalid; must be one of: shiny, quarto, dashboard, report, other", i, ex.Name, ex.Type))
		}
	}
	return errs
}

// validateCycles runs cycle detection only when no prior errors exist.
func validateCycles(d *DAG, priorErrs []string) []string {
	if len(priorErrs) > 0 {
		return nil
	}
	if _, err := TopoSort(d.Steps); err != nil {
		return []string{err.Error()}
	}
	return nil
}

// validateRVersionConstraint checks that a version constraint string is valid.
// Supported forms: ">=4.1.0", "==4.4.1"
func validateRVersionConstraint(constraint string) error {
	constraint = strings.TrimSpace(constraint)
	if strings.HasPrefix(constraint, ">=") {
		v := strings.TrimPrefix(constraint, ">=")
		return validateVersionString(v)
	}
	if strings.HasPrefix(constraint, "==") {
		v := strings.TrimPrefix(constraint, "==")
		return validateVersionString(v)
	}
	return fmt.Errorf("must start with >= or ==")
}

func validateVersionString(v string) error {
	parts := strings.Split(strings.TrimSpace(v), ".")
	if len(parts) < 2 || len(parts) > 3 {
		return fmt.Errorf("version must have 2 or 3 parts (e.g. 4.1 or 4.1.0)")
	}
	for _, p := range parts {
		for _, c := range p {
			if c < '0' || c > '9' {
				return fmt.Errorf("version part %q is not numeric", p)
			}
		}
	}
	return nil
}

// CheckRVersion checks if an R version satisfies the DAG's constraint.
// Returns a human-readable message and whether the check passed.
func CheckRVersion(constraint, actual string) (string, bool) {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" || actual == "" {
		return "", true
	}

	var op, required string
	switch {
	case strings.HasPrefix(constraint, ">="):
		op = ">="
		required = strings.TrimSpace(strings.TrimPrefix(constraint, ">="))
	case strings.HasPrefix(constraint, "=="):
		op = "=="
		required = strings.TrimSpace(strings.TrimPrefix(constraint, "=="))
	default:
		return fmt.Sprintf("unknown r_version operator in %q", constraint), false
	}

	cmp := compareVersions(actual, required)
	switch op {
	case ">=":
		if cmp >= 0 {
			return "", true
		}
		return fmt.Sprintf("R %s does not satisfy %s (have %s)", actual, constraint, actual), false
	case "==":
		if cmp == 0 {
			return "", true
		}
		return fmt.Sprintf("R %s does not satisfy %s (have %s)", actual, constraint, actual), false
	}
	return "", true
}

// compareVersions compares two version strings (e.g. "4.4.1" vs "4.1.0").
// Returns -1 if a < b, 0 if equal, 1 if a > b.
func compareVersions(a, b string) int {
	pa := strings.Split(a, ".")
	pb := strings.Split(b, ".")

	for i := 0; i < 3; i++ {
		va, vb := 0, 0
		if i < len(pa) {
			for _, c := range pa[i] {
				if c >= '0' && c <= '9' {
					va = va*10 + int(c-'0')
				}
			}
		}
		if i < len(pb) {
			for _, c := range pb[i] {
				if c >= '0' && c <= '9' {
					vb = vb*10 + int(c-'0')
				}
			}
		}
		if va < vb {
			return -1
		}
		if va > vb {
			return 1
		}
	}
	return 0
}

// validateDeadlineTime checks that an HH:MM string has valid hour (00-23) and minute (00-59) values.
func validateDeadlineTime(s string) error {
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil {
		return fmt.Errorf("cannot parse time: %w", err)
	}
	if h < 0 || h > 23 {
		return fmt.Errorf("hour %d out of range 0-23", h)
	}
	if m < 0 || m > 59 {
		return fmt.Errorf("minute %d out of range 0-59", m)
	}
	return nil
}
