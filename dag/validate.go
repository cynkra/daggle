package dag

import (
	"fmt"
	"strings"
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
		if s.Script != "" {
			typeCount++
		}
		if s.RExpr != "" {
			typeCount++
		}
		if s.Command != "" {
			typeCount++
		}
		if typeCount == 0 {
			errs = append(errs, fmt.Sprintf("step %q must have one of: script, r_expr, command", s.ID))
		}
		if typeCount > 1 {
			errs = append(errs, fmt.Sprintf("step %q has multiple types; use exactly one of: script, r_expr, command", s.ID))
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
		if s.Retry != nil && s.Retry.Limit < 0 {
			errs = append(errs, fmt.Sprintf("step %q retry limit must be >= 0", s.ID))
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
