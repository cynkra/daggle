package dag

import "testing"

func TestExpandMatrix(t *testing.T) {
	steps := []Step{
		{ID: "prepare", Command: "echo prep"},
		{
			ID:      "model",
			Command: "echo model",
			Matrix:  map[string][]string{"algo": {"lm", "glm"}},
			Args:    []string{"--algo", "{{ .Matrix.algo }}"},
			Depends: []string{"prepare"},
		},
		{ID: "compare", Command: "echo compare", Depends: []string{"model"}},
	}

	expanded := ExpandMatrix(steps)

	// Should have: prepare + 2 model instances + compare = 4 steps
	if len(expanded) != 4 {
		t.Fatalf("expanded = %d steps, want 4", len(expanded))
	}

	// prepare unchanged
	if expanded[0].ID != "prepare" {
		t.Errorf("step 0 = %q, want %q", expanded[0].ID, "prepare")
	}

	// Two model instances with matrix-derived IDs
	modelIDs := map[string]bool{}
	for _, s := range expanded[1:3] {
		modelIDs[s.ID] = true
		// Should have DAGGLE_MATRIX_ALGO env var
		if s.Env["DAGGLE_MATRIX_ALGO"].Value == "" {
			t.Errorf("step %q missing DAGGLE_MATRIX_ALGO", s.ID)
		}
		// Args should have been interpolated
		if len(s.Args) > 0 && s.Args[1] != "lm" && s.Args[1] != "glm" {
			t.Errorf("step %q args not interpolated: %v", s.ID, s.Args)
		}
	}
	if len(modelIDs) != 2 {
		t.Errorf("expected 2 unique model instance IDs, got %v", modelIDs)
	}

	// compare should depend on both model instances
	if len(expanded[3].Depends) != 2 {
		t.Errorf("compare depends = %v, want 2 entries", expanded[3].Depends)
	}
}

func TestMatrixCombinations(t *testing.T) {
	combos := matrixCombinations(map[string][]string{
		"a": {"1", "2"},
		"b": {"x"},
	})
	if len(combos) != 2 {
		t.Fatalf("combos = %d, want 2", len(combos))
	}
}

func TestMatrixCombinations_Deterministic(t *testing.T) {
	matrix := map[string][]string{
		"z": {"3", "4"},
		"a": {"1", "2"},
		"m": {"x", "y"},
	}
	first := matrixCombinations(matrix)
	for i := 0; i < 20; i++ {
		again := matrixCombinations(matrix)
		if len(again) != len(first) {
			t.Fatalf("length mismatch on iteration %d", i)
		}
		for j, combo := range again {
			for k, v := range combo {
				if first[j][k] != v {
					t.Fatalf("non-deterministic output at iteration %d, combo %d: key %q got %q, want %q", i, j, k, v, first[j][k])
				}
			}
		}
	}
}
