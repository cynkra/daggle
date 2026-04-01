package dag

import (
	"strings"
	"testing"
)

func TestParseFile_Simple(t *testing.T) {
	d, err := ParseFile("testdata/simple.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if d.Name != "simple-dag" {
		t.Errorf("name = %q, want %q", d.Name, "simple-dag")
	}
	if len(d.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(d.Steps))
	}
	if d.Env["OUTPUT_DIR"] != "/tmp/output" {
		t.Errorf("env OUTPUT_DIR = %q, want %q", d.Env["OUTPUT_DIR"], "/tmp/output")
	}
	if len(d.Params) != 1 || d.Params[0].Name != "department" {
		t.Errorf("params = %+v, want [{Name:department Default:sales}]", d.Params)
	}
}

func TestParseFile_Cycle(t *testing.T) {
	_, err := ParseFile("testdata/cycle.yaml")
	if err == nil {
		t.Fatal("expected error for cycle, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error = %q, want it to mention cycle", err.Error())
	}
}

func TestParseFile_Invalid(t *testing.T) {
	_, err := ParseFile("testdata/invalid.yaml")
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	errMsg := err.Error()
	for _, want := range []string{"name is required", "must have one of", "multiple types", "invalid timeout", "unknown step"} {
		if !strings.Contains(errMsg, want) {
			t.Errorf("error missing %q, got: %s", want, errMsg)
		}
	}
}

func TestStepType(t *testing.T) {
	tests := []struct {
		step Step
		want string
	}{
		{Step{Script: "foo.R"}, "script"},
		{Step{RExpr: "1+1"}, "r_expr"},
		{Step{Command: "echo hi"}, "command"},
		{Step{}, ""},
	}
	for _, tt := range tests {
		if got := StepType(tt.step); got != tt.want {
			t.Errorf("StepType(%+v) = %q, want %q", tt.step, got, tt.want)
		}
	}
}

func TestMaxAttempts(t *testing.T) {
	s1 := Step{}
	if got := s1.MaxAttempts(); got != 1 {
		t.Errorf("no retry: MaxAttempts = %d, want 1", got)
	}

	s2 := Step{Retry: &Retry{Limit: 3}}
	if got := s2.MaxAttempts(); got != 4 {
		t.Errorf("retry 3: MaxAttempts = %d, want 4", got)
	}
}

func TestTopoSort_Linear(t *testing.T) {
	steps := []Step{
		{ID: "a"},
		{ID: "b", Depends: []string{"a"}},
		{ID: "c", Depends: []string{"b"}},
	}
	tiers, err := TopoSort(steps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tiers) != 3 {
		t.Fatalf("tiers = %d, want 3", len(tiers))
	}
	if tiers[0][0].ID != "a" || tiers[1][0].ID != "b" || tiers[2][0].ID != "c" {
		t.Errorf("unexpected tier order: %v", tiers)
	}
}

func TestTopoSort_Parallel(t *testing.T) {
	steps := []Step{
		{ID: "a"},
		{ID: "b"},
		{ID: "c", Depends: []string{"a", "b"}},
	}
	tiers, err := TopoSort(steps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tiers) != 2 {
		t.Fatalf("tiers = %d, want 2", len(tiers))
	}
	if len(tiers[0]) != 2 {
		t.Errorf("tier 0 size = %d, want 2", len(tiers[0]))
	}
}

func TestTopoSort_Cycle(t *testing.T) {
	steps := []Step{
		{ID: "a", Depends: []string{"c"}},
		{ID: "b", Depends: []string{"a"}},
		{ID: "c", Depends: []string{"b"}},
	}
	_, err := TopoSort(steps)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
}

func TestExpandDAG(t *testing.T) {
	d := &DAG{
		Name: "test",
		Env:  map[string]string{"DATE": "{{ .Today }}"},
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
