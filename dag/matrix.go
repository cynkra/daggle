package dag

import (
	"fmt"
	"strings"
)

// ExpandMatrix expands steps with matrix definitions into multiple concrete steps.
// Each matrix step becomes N steps (one per combination), with IDs like "step-key1_val1-key2_val2".
// Non-matrix steps are passed through unchanged.
func ExpandMatrix(steps []Step) []Step {
	// First pass: expand matrix steps and collect ID mappings
	replacements := make(map[string][]string) // original ID → expanded IDs
	var expanded []Step

	for _, s := range steps {
		if len(s.Matrix) == 0 {
			expanded = append(expanded, s)
			continue
		}

		combos := matrixCombinations(s.Matrix)
		originalID := s.ID
		var instanceIDs []string

		for _, combo := range combos {
			instance := s
			instance.Matrix = nil

			var parts []string
			for k, v := range combo {
				parts = append(parts, fmt.Sprintf("%s_%s", k, v))
			}
			instance.ID = originalID + "-" + strings.Join(parts, "-")
			instanceIDs = append(instanceIDs, instance.ID)

			if instance.Env == nil {
				instance.Env = make(map[string]string)
			}
			for k, v := range combo {
				instance.Env["DAGGLE_MATRIX_"+strings.ToUpper(k)] = v
			}

			// Make a copy of args to avoid sharing the slice
			if len(s.Args) > 0 {
				instance.Args = make([]string, len(s.Args))
				copy(instance.Args, s.Args)
			}
			for i, arg := range instance.Args {
				for k, v := range combo {
					arg = strings.ReplaceAll(arg, "{{ .Matrix."+k+" }}", v)
					arg = strings.ReplaceAll(arg, "{{.Matrix."+k+"}}", v)
				}
				instance.Args[i] = arg
			}

			expanded = append(expanded, instance)
		}

		replacements[originalID] = instanceIDs
	}

	// Second pass: update dependencies to reference expanded instance IDs
	for i := range expanded {
		var newDeps []string
		for _, dep := range expanded[i].Depends {
			if ids, ok := replacements[dep]; ok {
				newDeps = append(newDeps, ids...)
			} else {
				newDeps = append(newDeps, dep)
			}
		}
		expanded[i].Depends = newDeps
	}

	return expanded
}

// matrixCombinations generates all combinations from a matrix definition.
// e.g. {"a": ["1","2"], "b": ["x","y"]} → [{"a":"1","b":"x"}, {"a":"1","b":"y"}, ...]
func matrixCombinations(matrix map[string][]string) []map[string]string {
	// Get sorted keys for deterministic output
	var keys []string
	for k := range matrix {
		keys = append(keys, k)
	}

	// Generate combinations via cartesian product
	combos := []map[string]string{{}}
	for _, key := range keys {
		vals := matrix[key]
		var newCombos []map[string]string
		for _, combo := range combos {
			for _, val := range vals {
				newCombo := make(map[string]string, len(combo)+1)
				for k, v := range combo {
					newCombo[k] = v
				}
				newCombo[key] = val
				newCombos = append(newCombos, newCombo)
			}
		}
		combos = newCombos
	}
	return combos
}
