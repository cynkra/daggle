package dag

import (
	"fmt"
	"sort"
	"strings"
)

// ExpandMatrix expands steps with matrix definitions into multiple concrete steps.
// Each matrix step becomes N steps (one per combination), with IDs like "step-key1_val1-key2_val2".
// Non-matrix steps are passed through unchanged.
func ExpandMatrix(steps []Step) []Step {
	// First pass: expand matrix steps and collect ID mappings
	replacements := make(map[string][]string) // original ID → expanded IDs
	expanded := make([]Step, 0, len(steps))

	for _, s := range steps {
		if len(s.Matrix) == 0 {
			expanded = append(expanded, s)
			continue
		}

		combos := matrixCombinations(s.Matrix)
		originalID := s.ID
		instanceIDs := make([]string, 0, len(combos))

		for _, combo := range combos {
			instance := s
			instance.Matrix = nil

			parts := make([]string, 0, len(combo))
			for k, v := range combo {
				parts = append(parts, fmt.Sprintf("%s_%s", k, v))
			}
			instance.ID = originalID + "-" + strings.Join(parts, "-")
			instanceIDs = append(instanceIDs, instance.ID)

			if instance.Env == nil {
				instance.Env = make(EnvMap, len(combo))
			}
			for k, v := range combo {
				instance.Env["DAGGLE_MATRIX_"+strings.ToUpper(k)] = EnvVar{Value: v}
			}

			// Build a single Replacer for this combo that handles both the
			// "{{ .Matrix.X }}" and "{{.Matrix.X}}" forms in one pass.
			rep := matrixReplacer(combo)

			if len(s.Args) > 0 {
				instance.Args = make([]string, len(s.Args))
				for i, arg := range s.Args {
					instance.Args[i] = rep.Replace(arg)
				}
			}

			instance.OutputDir = rep.Replace(instance.OutputDir)
			instance.OutputName = rep.Replace(instance.OutputName)

			expanded = append(expanded, instance)
		}

		replacements[originalID] = instanceIDs
	}

	// Second pass: update dependencies to reference expanded instance IDs.
	for i := range expanded {
		deps := expanded[i].Depends
		if len(deps) == 0 {
			continue
		}
		newDeps := make([]string, 0, len(deps))
		for _, dep := range deps {
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

// matrixReplacer builds a strings.Replacer that substitutes every
// "{{ .Matrix.KEY }}" and "{{.Matrix.KEY}}" token with the corresponding
// value from combo, in a single linear scan per Replace call.
func matrixReplacer(combo map[string]string) *strings.Replacer {
	pairs := make([]string, 0, len(combo)*4)
	for k, v := range combo {
		pairs = append(pairs,
			"{{ .Matrix."+k+" }}", v,
			"{{.Matrix."+k+"}}", v,
		)
	}
	return strings.NewReplacer(pairs...)
}

// matrixCombinations generates all combinations from a matrix definition.
// e.g. {"a": ["1","2"], "b": ["x","y"]} → [{"a":"1","b":"x"}, {"a":"1","b":"y"}, ...]
func matrixCombinations(matrix map[string][]string) []map[string]string {
	// Get sorted keys for deterministic output
	keys := make([]string, 0, len(matrix))
	for k := range matrix {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Preallocate the result slice to the exact cartesian-product size.
	total := 1
	for _, key := range keys {
		total *= len(matrix[key])
	}
	combos := make([]map[string]string, 0, total)
	combos = append(combos, map[string]string{})
	for _, key := range keys {
		vals := matrix[key]
		newCombos := make([]map[string]string, 0, len(combos)*len(vals))
		for _, combo := range combos {
			for _, val := range vals {
				newCombo := make(map[string]string, len(keys))
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
