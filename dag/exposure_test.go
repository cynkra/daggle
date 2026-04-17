package dag

import (
	"strings"
	"testing"
)

func TestValidateExposures(t *testing.T) {
	cases := []struct {
		name    string
		exps    []Exposure
		wantErr string
	}{
		{"none", nil, ""},
		{"ok", []Exposure{{Name: "dash", Type: "dashboard"}}, ""},
		{"missing name", []Exposure{{Type: "shiny"}}, "name is required"},
		{"missing type", []Exposure{{Name: "x"}}, "type is required"},
		{"bad type", []Exposure{{Name: "x", Type: "slack"}}, "type \"slack\" is invalid"},
		{"duplicate", []Exposure{{Name: "x", Type: "shiny"}, {Name: "x", Type: "quarto"}}, `duplicate name "x"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &DAG{Name: "t", Steps: []Step{{ID: "a", Command: "echo"}}, Exposures: tc.exps}
			err := Validate(d)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want contains %q", err, tc.wantErr)
			}
		})
	}
}
