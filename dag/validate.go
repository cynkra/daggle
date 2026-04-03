package dag

import (
	"fmt"
	"strings"
	"time"
)

// Validate checks that a DAG definition is well-formed.
func Validate(d *DAG) error {
	var errs []string

	if d.Name == "" {
		errs = append(errs, "dag name is required")
	}

	if len(d.Steps) == 0 {
		errs = append(errs, "dag must have at least one step")
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

		// Exactly one step type must be set
		typeCount := 0
		for _, set := range []bool{
			s.Script != "", s.RExpr != "", s.Command != "", s.Quarto != "",
			s.Test != "", s.Check != "", s.Document != "", s.Lint != "", s.Style != "",
			s.Rmd != "", s.RenvRestore != "", s.Coverage != "", s.Validate != "",
			s.Pin != nil, s.Vetiver != nil,
			s.Shinytest != "", s.Pkgdown != "", s.Install != "", s.Targets != "",
			s.Benchmark != "", s.Revdepcheck != "",
			s.Connect != nil,
		} {
			if set {
				typeCount++
			}
		}
		stepTypes := "script, r_expr, command, quarto, test, check, document, lint, style, rmd, renv_restore, coverage, validate, pin, vetiver, shinytest, pkgdown, install, targets, benchmark, revdepcheck, connect"
		if typeCount == 0 {
			errs = append(errs, fmt.Sprintf("step %q must have one of: %s", s.ID, stepTypes))
		}
		if typeCount > 1 {
			errs = append(errs, fmt.Sprintf("step %q has multiple types; use exactly one of: %s", s.ID, stepTypes))
		}

		// Validate connect step fields
		if s.Connect != nil {
			validTypes := map[string]bool{"shiny": true, "quarto": true, "plumber": true}
			if s.Connect.Type == "" {
				errs = append(errs, fmt.Sprintf("step %q connect.type is required (shiny, quarto, plumber)", s.ID))
			} else if !validTypes[s.Connect.Type] {
				errs = append(errs, fmt.Sprintf("step %q connect.type %q is invalid; must be one of: shiny, quarto, plumber", s.ID, s.Connect.Type))
			}
			if s.Connect.Path == "" {
				errs = append(errs, fmt.Sprintf("step %q connect.path is required", s.ID))
			}
		}

		// Validate depends references
		for _, dep := range s.Depends {
			if !ids[dep] {
				// Check if it exists anywhere in the step list (forward reference)
				found := false
				for _, other := range d.Steps {
					if other.ID == dep {
						found = true
						break
					}
				}
				if !found {
					errs = append(errs, fmt.Sprintf("step %q depends on unknown step %q", s.ID, dep))
				}
			}
		}

		// Validate timeout
		if s.Timeout != "" {
			if _, err := s.ParseTimeout(); err != nil {
				errs = append(errs, fmt.Sprintf("step %q has invalid timeout %q: %v", s.ID, s.Timeout, err))
			}
		}

		// Validate error_on
		if s.ErrorOn != "" {
			validErrorOn := map[string]bool{"error": true, "warning": true, "message": true}
			if !validErrorOn[s.ErrorOn] {
				errs = append(errs, fmt.Sprintf("step %q error_on %q is invalid; must be one of: error, warning, message", s.ID, s.ErrorOn))
			}
		}

		// Validate retry
		if s.Retry != nil {
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
		}
	}

	// Validate r_version constraint
	if d.RVersion != "" {
		if err := validateRVersionConstraint(d.RVersion); err != nil {
			errs = append(errs, fmt.Sprintf("r_version %q is invalid: %v", d.RVersion, err))
		}
	}

	// Validate trigger block
	if d.Trigger != nil {
		if d.Trigger.Overlap != "" && d.Trigger.Overlap != "skip" && d.Trigger.Overlap != "cancel" {
			errs = append(errs, fmt.Sprintf("trigger.overlap %q is invalid; must be one of: skip, cancel", d.Trigger.Overlap))
		}
		if d.Trigger.Watch != nil {
			if d.Trigger.Watch.Path == "" {
				errs = append(errs, "trigger.watch.path is required")
			}
			if d.Trigger.Watch.Debounce != "" {
				if _, err := time.ParseDuration(d.Trigger.Watch.Debounce); err != nil {
					errs = append(errs, fmt.Sprintf("trigger.watch.debounce %q is invalid: %v", d.Trigger.Watch.Debounce, err))
				}
			}
		}
		if d.Trigger.OnDAG != nil {
			if d.Trigger.OnDAG.Name == "" {
				errs = append(errs, "trigger.on_dag.name is required")
			}
			validStatuses := map[string]bool{"": true, "completed": true, "failed": true, "any": true}
			if !validStatuses[d.Trigger.OnDAG.Status] {
				errs = append(errs, fmt.Sprintf("trigger.on_dag.status %q is invalid; must be one of: completed, failed, any", d.Trigger.OnDAG.Status))
			}
		}
		if d.Trigger.Condition != nil {
			if d.Trigger.Condition.RExpr == "" && d.Trigger.Condition.Command == "" {
				errs = append(errs, "trigger.condition requires r_expr or command")
			}
			if d.Trigger.Condition.PollInterval != "" {
				if _, err := time.ParseDuration(d.Trigger.Condition.PollInterval); err != nil {
					errs = append(errs, fmt.Sprintf("trigger.condition.poll_interval %q is invalid: %v", d.Trigger.Condition.PollInterval, err))
				}
			}
		}
		if d.Trigger.Git != nil {
			if d.Trigger.Git.PollInterval != "" {
				if _, err := time.ParseDuration(d.Trigger.Git.PollInterval); err != nil {
					errs = append(errs, fmt.Sprintf("trigger.git.poll_interval %q is invalid: %v", d.Trigger.Git.PollInterval, err))
				}
			}
			validEvents := map[string]bool{"push": true, "tag": true}
			for _, ev := range d.Trigger.Git.Events {
				if !validEvents[ev] {
					errs = append(errs, fmt.Sprintf("trigger.git.events contains invalid event %q; must be one of: push, tag", ev))
				}
			}
		}
	}

	// Cycle detection via TopoSort
	if len(errs) == 0 {
		if _, err := TopoSort(d.Steps); err != nil {
			errs = append(errs, err.Error())
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("validation errors:\n  - %s", strings.Join(errs, "\n  - "))
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
	if strings.HasPrefix(constraint, ">=") {
		op = ">="
		required = strings.TrimSpace(strings.TrimPrefix(constraint, ">="))
	} else if strings.HasPrefix(constraint, "==") {
		op = "=="
		required = strings.TrimSpace(strings.TrimPrefix(constraint, "=="))
	} else {
		return fmt.Sprintf("unknown r_version operator in %q", constraint), false
	}

	cmp := compareVersions(actual, required)
	switch op {
	case ">=":
		if cmp >= 0 {
			return "", true
		}
		return fmt.Sprintf("R version %s does not satisfy %s (have %s)", constraint, constraint, actual), false
	case "==":
		if cmp == 0 {
			return "", true
		}
		return fmt.Sprintf("R version %s does not satisfy %s (have %s)", constraint, constraint, actual), false
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
