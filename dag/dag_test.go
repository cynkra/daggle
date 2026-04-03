package dag

import (
	"os"
	"path/filepath"
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
		{Step{Quarto: "report.qmd"}, "quarto"},
		{Step{Test: "."}, "test"},
		{Step{Check: "."}, "check"},
		{Step{Document: "."}, "document"},
		{Step{Lint: "."}, "lint"},
		{Step{Style: "."}, "style"},
		{Step{Connect: &ConnectDeploy{Type: "shiny", Path: "app/"}}, "connect"},
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

func TestValidate_NewStepTypes(t *testing.T) {
	// Valid quarto step
	d := &DAG{Name: "t", Steps: []Step{{ID: "a", Quarto: "report.qmd"}}}
	if err := Validate(d); err != nil {
		t.Errorf("quarto step should be valid: %v", err)
	}

	// Valid connect step
	d = &DAG{Name: "t", Steps: []Step{{ID: "a", Connect: &ConnectDeploy{Type: "shiny", Path: "app/"}}}}
	if err := Validate(d); err != nil {
		t.Errorf("connect step should be valid: %v", err)
	}

	// Connect missing type
	d = &DAG{Name: "t", Steps: []Step{{ID: "a", Connect: &ConnectDeploy{Path: "app/"}}}}
	if err := Validate(d); err == nil {
		t.Error("connect without type should fail")
	}

	// Connect invalid type
	d = &DAG{Name: "t", Steps: []Step{{ID: "a", Connect: &ConnectDeploy{Type: "invalid", Path: "app/"}}}}
	if err := Validate(d); err == nil {
		t.Error("connect with invalid type should fail")
	}

	// Connect missing path
	d = &DAG{Name: "t", Steps: []Step{{ID: "a", Connect: &ConnectDeploy{Type: "shiny"}}}}
	if err := Validate(d); err == nil {
		t.Error("connect without path should fail")
	}

	// Multiple types should fail
	d = &DAG{Name: "t", Steps: []Step{{ID: "a", Script: "foo.R", Quarto: "bar.qmd"}}}
	if err := Validate(d); err == nil {
		t.Error("multiple types should fail")
	}
}

func TestValidate_RetryBackoff(t *testing.T) {
	// Valid exponential
	d := &DAG{Name: "t", Steps: []Step{{ID: "a", Command: "echo", Retry: &Retry{Limit: 2, Backoff: "exponential"}}}}
	if err := Validate(d); err != nil {
		t.Errorf("exponential backoff should be valid: %v", err)
	}

	// Valid with max_delay
	d = &DAG{Name: "t", Steps: []Step{{ID: "a", Command: "echo", Retry: &Retry{Limit: 2, Backoff: "exponential", MaxDelay: "30s"}}}}
	if err := Validate(d); err != nil {
		t.Errorf("max_delay should be valid: %v", err)
	}

	// Invalid backoff
	d = &DAG{Name: "t", Steps: []Step{{ID: "a", Command: "echo", Retry: &Retry{Limit: 2, Backoff: "quadratic"}}}}
	if err := Validate(d); err == nil {
		t.Error("invalid backoff should fail")
	}

	// Invalid max_delay
	d = &DAG{Name: "t", Steps: []Step{{ID: "a", Command: "echo", Retry: &Retry{Limit: 2, MaxDelay: "not-a-duration"}}}}
	if err := Validate(d); err == nil {
		t.Error("invalid max_delay should fail")
	}
}

func TestTriggerHelpers(t *testing.T) {
	// No trigger
	d1 := &DAG{Name: "no-trigger"}
	if d1.HasTrigger() {
		t.Error("expected HasTrigger()=false for nil trigger")
	}
	if d1.CronSchedule() != "" {
		t.Error("expected empty CronSchedule() for nil trigger")
	}

	// Schedule trigger
	d2 := &DAG{Name: "cron", Trigger: &Trigger{Schedule: "@every 1h"}}
	if !d2.HasTrigger() {
		t.Error("expected HasTrigger()=true for schedule trigger")
	}
	if d2.CronSchedule() != "@every 1h" {
		t.Errorf("CronSchedule() = %q, want %q", d2.CronSchedule(), "@every 1h")
	}

	// Watch trigger
	d3 := &DAG{Name: "watch", Trigger: &Trigger{Watch: &WatchTrigger{Path: "/data"}}}
	if !d3.HasTrigger() {
		t.Error("expected HasTrigger()=true for watch trigger")
	}
	if d3.CronSchedule() != "" {
		t.Error("expected empty CronSchedule() for watch-only trigger")
	}
}

func TestValidate_TriggerBlock(t *testing.T) {
	base := func() *DAG {
		return &DAG{
			Name:  "test",
			Steps: []Step{{ID: "a", Command: "echo a"}},
		}
	}

	// Valid: no trigger
	if err := Validate(base()); err != nil {
		t.Errorf("no trigger should be valid: %v", err)
	}

	// Valid: schedule trigger
	d := base()
	d.Trigger = &Trigger{Schedule: "@every 1h"}
	if err := Validate(d); err != nil {
		t.Errorf("schedule trigger should be valid: %v", err)
	}

	// Invalid: watch without path
	d = base()
	d.Trigger = &Trigger{Watch: &WatchTrigger{}}
	if err := Validate(d); err == nil {
		t.Error("watch without path should fail")
	}

	// Invalid: on_dag without name
	d = base()
	d.Trigger = &Trigger{OnDAG: &OnDAGTrigger{}}
	if err := Validate(d); err == nil {
		t.Error("on_dag without name should fail")
	}

	// Invalid: on_dag with bad status
	d = base()
	d.Trigger = &Trigger{OnDAG: &OnDAGTrigger{Name: "upstream", Status: "bogus"}}
	if err := Validate(d); err == nil {
		t.Error("on_dag with invalid status should fail")
	}

	// Invalid: condition without r_expr or command
	d = base()
	d.Trigger = &Trigger{Condition: &ConditionTrigger{}}
	if err := Validate(d); err == nil {
		t.Error("condition without r_expr or command should fail")
	}

	// Invalid: git with bad event
	d = base()
	d.Trigger = &Trigger{Git: &GitTrigger{Events: []string{"push", "invalid"}}}
	if err := Validate(d); err == nil {
		t.Error("git with invalid event should fail")
	}

	// Invalid: bad debounce duration
	d = base()
	d.Trigger = &Trigger{Watch: &WatchTrigger{Path: "/data", Debounce: "not-a-duration"}}
	if err := Validate(d); err == nil {
		t.Error("watch with invalid debounce should fail")
	}
}

func TestResolveWorkdir(t *testing.T) {
	tests := []struct {
		name string
		dag  DAG
		step Step
		want string
	}{
		{"step wins", DAG{Workdir: "/dag", SourceDir: "/src"}, Step{Workdir: "/step"}, "/step"},
		{"dag wins over source", DAG{Workdir: "/dag", SourceDir: "/src"}, Step{}, "/dag"},
		{"source fallback", DAG{SourceDir: "/src"}, Step{}, "/src"},
		{"empty", DAG{}, Step{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.dag.ResolveWorkdir(tt.step); got != tt.want {
				t.Errorf("ResolveWorkdir() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHashFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")

	content := []byte("name: test\nsteps:\n  - id: a\n    command: echo\n")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	h1, err := HashFile(path)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	if len(h1) != 64 { // SHA-256 hex = 64 chars
		t.Errorf("hash length = %d, want 64", len(h1))
	}

	// Same content = same hash
	h2, err := HashFile(path)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	if h1 != h2 {
		t.Error("same file should produce same hash")
	}

	// Modified content = different hash
	if err := os.WriteFile(path, []byte("name: changed\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h3, err := HashFile(path)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	if h1 == h3 {
		t.Error("different content should produce different hash")
	}

	// Nonexistent file
	_, err = HashFile(filepath.Join(dir, "nonexistent"))
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestStepType_NewTypes(t *testing.T) {
	tests := []struct {
		step Step
		want string
	}{
		{Step{Rmd: "report.Rmd"}, "rmd"},
		{Step{RenvRestore: "."}, "renv_restore"},
		{Step{Coverage: "."}, "coverage"},
		{Step{Validate: "rules.R"}, "validate"},
	}
	for _, tt := range tests {
		if got := StepType(tt.step); got != tt.want {
			t.Errorf("StepType(%+v) = %q, want %q", tt.step, got, tt.want)
		}
	}
}

func TestValidate_ErrorOn(t *testing.T) {
	// Valid error_on values
	for _, val := range []string{"error", "warning", "message"} {
		d := &DAG{Name: "t", Steps: []Step{{ID: "a", Command: "echo", ErrorOn: val}}}
		if err := Validate(d); err != nil {
			t.Errorf("error_on=%q should be valid: %v", val, err)
		}
	}

	// Invalid error_on
	d := &DAG{Name: "t", Steps: []Step{{ID: "a", Command: "echo", ErrorOn: "panic"}}}
	if err := Validate(d); err == nil {
		t.Error("error_on=panic should fail validation")
	}
}

func TestValidate_RVersion(t *testing.T) {
	// Valid constraints
	for _, v := range []string{">=4.1.0", ">=4.1", "==4.4.1"} {
		d := &DAG{Name: "t", RVersion: v, Steps: []Step{{ID: "a", Command: "echo"}}}
		if err := Validate(d); err != nil {
			t.Errorf("r_version=%q should be valid: %v", v, err)
		}
	}

	// Invalid constraints
	for _, v := range []string{"4.1.0", ">4.1.0", ">=abc"} {
		d := &DAG{Name: "t", RVersion: v, Steps: []Step{{ID: "a", Command: "echo"}}}
		if err := Validate(d); err == nil {
			t.Errorf("r_version=%q should fail validation", v)
		}
	}
}

func TestCheckRVersion(t *testing.T) {
	tests := []struct {
		constraint string
		actual     string
		wantOK     bool
	}{
		{">=4.1.0", "4.4.1", true},
		{">=4.1.0", "4.1.0", true},
		{">=4.1.0", "4.0.5", false},
		{">=4.1", "4.1.0", true},
		{"==4.4.1", "4.4.1", true},
		{"==4.4.1", "4.4.0", false},
		{"", "4.4.1", true},  // empty constraint always passes
		{">=4.1.0", "", true}, // no R version always passes
	}
	for _, tt := range tests {
		_, ok := CheckRVersion(tt.constraint, tt.actual)
		if ok != tt.wantOK {
			t.Errorf("CheckRVersion(%q, %q) = %v, want %v", tt.constraint, tt.actual, ok, tt.wantOK)
		}
	}
}

func TestDAG_VersionField(t *testing.T) {
	// Version field is optional and accepted
	d := &DAG{Name: "t", Version: "1", Steps: []Step{{ID: "a", Command: "echo"}}}
	if err := Validate(d); err != nil {
		t.Errorf("version field should be valid: %v", err)
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
