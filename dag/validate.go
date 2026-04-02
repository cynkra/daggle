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
			s.Connect != nil,
		} {
			if set {
				typeCount++
			}
		}
		stepTypes := "script, r_expr, command, quarto, test, check, document, lint, style, rmd, renv_restore, coverage, validate, connect"
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
