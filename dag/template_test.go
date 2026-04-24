package dag

import "testing"

func TestExpandDAG(t *testing.T) {
	d := &DAG{
		Name: "test",
		Env:  EnvMap{"DATE": {Value: "{{ .Today }}"}},
		Params: []Param{
			{Name: "dept", Default: "sales"},
		},
		Steps: []Step{
			{
				ID:      "s1",
				Command: "echo {{ .Params.dept }}",
				Args:    []string{"--date", "{{ .Today }}"},
			},
		},
	}

	expanded, err := ExpandDAG(d, map[string]string{"dept": "marketing"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if expanded.Steps[0].Command != "echo marketing" {
		t.Errorf("command = %q, want %q", expanded.Steps[0].Command, "echo marketing")
	}
	if expanded.Steps[0].Args[0] != "--date" {
		t.Errorf("args[0] = %q, want %q", expanded.Steps[0].Args[0], "--date")
	}
	// Today should be a date string like 2026-04-01
	if len(expanded.Steps[0].Args[1]) != 10 {
		t.Errorf("args[1] = %q, want date string", expanded.Steps[0].Args[1])
	}
}
