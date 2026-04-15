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
	if d.Env["OUTPUT_DIR"].Value != "/tmp/output" {
		t.Errorf("env OUTPUT_DIR = %q, want %q", d.Env["OUTPUT_DIR"].Value, "/tmp/output")
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

func TestValidate_Deadline(t *testing.T) {
	base := func() *DAG {
		return &DAG{
			Name:  "test",
			Steps: []Step{{ID: "a", Command: "echo a"}},
		}
	}

	// Valid: deadline with on_deadline
	d := base()
	d.Trigger = &Trigger{
		Schedule: "@every 1h",
		Deadline: "08:00",
		OnDeadline: &Hook{Command: "echo missed"},
	}
	if err := Validate(d); err != nil {
		t.Errorf("valid deadline should pass: %v", err)
	}

	// Valid: deadline without on_deadline (just deadline, no hook)
	d = base()
	d.Trigger = &Trigger{Schedule: "@every 1h", Deadline: "23:59"}
	if err := Validate(d); err != nil {
		t.Errorf("deadline without on_deadline should be valid: %v", err)
	}

	// Invalid: single-digit hour "8:00"
	d = base()
	d.Trigger = &Trigger{Schedule: "@every 1h", Deadline: "8:00"}
	if err := Validate(d); err == nil {
		t.Error("deadline '8:00' should fail (must be HH:MM)")
	}

	// Invalid: hour out of range "25:00"
	d = base()
	d.Trigger = &Trigger{Schedule: "@every 1h", Deadline: "25:00"}
	if err := Validate(d); err == nil {
		t.Error("deadline '25:00' should fail (hour > 23)")
	}

	// Invalid: non-numeric "abc"
	d = base()
	d.Trigger = &Trigger{Schedule: "@every 1h", Deadline: "abc"}
	if err := Validate(d); err == nil {
		t.Error("deadline 'abc' should fail")
	}

	// Invalid: minute out of range "12:60"
	d = base()
	d.Trigger = &Trigger{Schedule: "@every 1h", Deadline: "12:60"}
	if err := Validate(d); err == nil {
		t.Error("deadline '12:60' should fail (minute > 59)")
	}

	// Invalid: on_deadline without deadline
	d = base()
	d.Trigger = &Trigger{
		Schedule:   "@every 1h",
		OnDeadline: &Hook{Command: "echo missed"},
	}
	if err := Validate(d); err == nil {
		t.Error("on_deadline without deadline should fail")
	}
}

func TestResolveWorkdir(t *testing.T) {
	tests := []struct {
		name string
		dag  DAG
		step Step
		want string
	}{
		{"absolute step wins", DAG{Workdir: "/dag", SourceDir: "/src"}, Step{Workdir: "/step"}, "/step"},
		{"dag wins over source", DAG{Workdir: "/dag", SourceDir: "/src"}, Step{}, "/dag"},
		{"source fallback", DAG{SourceDir: "/src"}, Step{}, "/src"},
		{"empty", DAG{}, Step{}, ""},
		{"relative step resolved against source", DAG{SourceDir: "/project"}, Step{Workdir: "subdir"}, "/project/subdir"},
		{"relative step resolved against dag workdir", DAG{Workdir: "/dag", SourceDir: "/src"}, Step{Workdir: "subdir"}, "/dag/subdir"},
		{"relative step no base", DAG{}, Step{Workdir: "subdir"}, "subdir"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.dag.ResolveWorkdir(tt.step); got != tt.want {
				t.Errorf("ResolveWorkdir() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseFile_SourceDir_DaggleDir(t *testing.T) {
	// DAG file inside .daggle/ → SourceDir should be the parent (project root)
	dir := t.TempDir()
	daggleDir := filepath.Join(dir, ".daggle")
	if err := os.MkdirAll(daggleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("name: test\nsteps:\n  - id: a\n    command: echo\n")
	dagPath := filepath.Join(daggleDir, "pipeline.yaml")
	if err := os.WriteFile(dagPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := ParseFile(dagPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if d.SourceDir != dir {
		t.Errorf("SourceDir = %q, want %q (project root)", d.SourceDir, dir)
	}
}

func TestParseFile_SourceDir_RegularDir(t *testing.T) {
	// DAG file in a regular directory → SourceDir should be that directory
	dir := t.TempDir()
	content := []byte("name: test\nsteps:\n  - id: a\n    command: echo\n")
	dagPath := filepath.Join(dir, "pipeline.yaml")
	if err := os.WriteFile(dagPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := ParseFile(dagPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if d.SourceDir != dir {
		t.Errorf("SourceDir = %q, want %q", d.SourceDir, dir)
	}
}

func TestHashFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")

	content := []byte("name: test\nsteps:\n  - id: a\n    command: echo\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
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
	if err := os.WriteFile(path, []byte("name: changed\n"), 0o644); err != nil {
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

func TestApplyBaseDefaults(t *testing.T) {
	base := &BaseDefaults{
		Env:     EnvMap{"BASE_VAR": {Value: "base_val"}, "SHARED": {Value: "from_base"}},
		Workdir: "/base/workdir",
		Timeout: "5m",
		Retry:   &Retry{Limit: 2},
		OnFailure: &Hook{Command: "echo base failed"},
	}

	d := &DAG{
		Name: "test",
		Env:  EnvMap{"SHARED": {Value: "from_dag"}, "DAG_VAR": {Value: "dag_val"}},
		Steps: []Step{
			{ID: "a", Command: "echo", Timeout: "10m"}, // has own timeout
			{ID: "b", Command: "echo"},                   // inherits base timeout/retry
		},
	}

	ApplyBaseDefaults(d, base)

	// Env: base merged, DAG wins on conflict
	if d.Env["BASE_VAR"].Value != "base_val" {
		t.Errorf("BASE_VAR = %q, want %q", d.Env["BASE_VAR"].Value, "base_val")
	}
	if d.Env["SHARED"].Value != "from_dag" {
		t.Errorf("SHARED = %q, want %q (DAG should win)", d.Env["SHARED"].Value, "from_dag")
	}

	// Workdir: base fills in when DAG is empty
	if d.Workdir != "/base/workdir" {
		t.Errorf("Workdir = %q, want %q", d.Workdir, "/base/workdir")
	}

	// Step a keeps its own timeout, step b gets base timeout
	if d.Steps[0].Timeout != "10m" {
		t.Errorf("step a timeout = %q, want %q (should keep own)", d.Steps[0].Timeout, "10m")
	}
	if d.Steps[1].Timeout != "5m" {
		t.Errorf("step b timeout = %q, want %q (should inherit base)", d.Steps[1].Timeout, "5m")
	}
	if d.Steps[1].Retry == nil || d.Steps[1].Retry.Limit != 2 {
		t.Error("step b should inherit base retry")
	}

	// Hook: base on_failure applied
	if d.OnFailure == nil || d.OnFailure.Command != "echo base failed" {
		t.Error("on_failure should come from base")
	}

	// Nil base is a no-op
	d2 := &DAG{Name: "test", Steps: []Step{{ID: "a", Command: "echo"}}}
	ApplyBaseDefaults(d2, nil)
	if d2.Env != nil {
		t.Error("nil base should not modify DAG")
	}
}

func TestLoadBaseDefaults(t *testing.T) {
	dir := t.TempDir()

	// No base.yaml — returns nil
	b, err := LoadBaseDefaults(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b != nil {
		t.Error("expected nil for missing base.yaml")
	}

	// Write a base.yaml
	content := `env:
  SHARED: base
timeout: "5m"
`
	if err := os.WriteFile(filepath.Join(dir, "base.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	b, err = LoadBaseDefaults(dir)
	if err != nil {
		t.Fatalf("LoadBaseDefaults: %v", err)
	}
	if b == nil {
		t.Fatal("expected non-nil base defaults")
	}
	if b.Env["SHARED"].Value != "base" {
		t.Errorf("env SHARED = %q, want %q", b.Env["SHARED"].Value, "base")
	}
	if b.Timeout != "5m" {
		t.Errorf("timeout = %q, want %q", b.Timeout, "5m")
	}
}

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

func TestEnvVar_UnmarshalYAML(t *testing.T) {
	// Plain string form
	yamlStr := `name: test
env:
  PLAIN: literal
  WITH_REF: "${env:FOO}"
steps:
  - id: a
    command: echo
`
	d, err := ParseReader(strings.NewReader(yamlStr))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.Env["PLAIN"].Value != "literal" {
		t.Errorf("PLAIN = %q, want %q", d.Env["PLAIN"].Value, "literal")
	}
	if d.Env["PLAIN"].Secret {
		t.Error("PLAIN should not be secret")
	}
	if d.Env["WITH_REF"].Value != "${env:FOO}" {
		t.Errorf("WITH_REF = %q", d.Env["WITH_REF"].Value)
	}
}

func TestEnvVar_UnmarshalYAML_MapForm(t *testing.T) {
	yamlStr := `name: test
env:
  DB_PASS:
    value: "${env:DATABASE_PASSWORD}"
    secret: true
  PLAIN:
    value: "literal"
steps:
  - id: a
    command: echo
`
	d, err := ParseReader(strings.NewReader(yamlStr))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !d.Env["DB_PASS"].Secret {
		t.Error("DB_PASS should be secret")
	}
	if d.Env["DB_PASS"].Value != "${env:DATABASE_PASSWORD}" {
		t.Errorf("DB_PASS value = %q", d.Env["DB_PASS"].Value)
	}
	if d.Env["PLAIN"].Secret {
		t.Error("PLAIN should not be secret")
	}
}

func TestRedactor(t *testing.T) {
	env := EnvMap{
		"PUBLIC":  {Value: "visible"},
		"SECRET1": {Value: "s3cr3t", Secret: true},
		"SECRET2": {Value: "p4ssw0rd", Secret: true},
	}
	r := NewRedactor(env)

	if !r.HasSecrets() {
		t.Error("should have secrets")
	}

	input := "Error: connection failed with password s3cr3t on host p4ssw0rd"
	redacted := r.Redact(input)
	if strings.Contains(redacted, "s3cr3t") || strings.Contains(redacted, "p4ssw0rd") {
		t.Errorf("redacted string still contains secrets: %q", redacted)
	}
	if !strings.Contains(redacted, "***") {
		t.Error("redacted string should contain ***")
	}

	// Nil redactor is safe
	var nilR *Redactor
	if nilR.Redact("test") != "test" {
		t.Error("nil redactor should pass through")
	}
}

func TestDAG_VersionField(t *testing.T) {
	// Version field is optional and accepted
	d := &DAG{Name: "t", Version: "1", Steps: []Step{{ID: "a", Command: "echo"}}}
	if err := Validate(d); err != nil {
		t.Errorf("version field should be valid: %v", err)
	}
}

func TestParseFile_Artifacts(t *testing.T) {
	d, err := ParseFile("testdata/artifacts.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(d.Steps[0].Artifacts) != 2 {
		t.Fatalf("step extract artifacts = %d, want 2", len(d.Steps[0].Artifacts))
	}
	art := d.Steps[0].Artifacts[0]
	if art.Name != "raw_data" || art.Path != "output/raw.parquet" || art.Format != "parquet" {
		t.Errorf("artifact 0 = %+v", art)
	}
	if d.Steps[0].Artifacts[1].Versioned != true {
		t.Error("artifact 1 should be versioned")
	}
}

func TestValidate_Artifacts(t *testing.T) {
	// Valid artifacts
	d := &DAG{Name: "t", Steps: []Step{{
		ID:      "a",
		Command: "echo",
		Artifacts: []Artifact{
			{Name: "output_data", Path: "out/data.csv"},
			{Name: "plot", Path: "out/plot.png", Format: "png"},
		},
	}}}
	if err := Validate(d); err != nil {
		t.Errorf("valid artifacts should pass: %v", err)
	}

	// Invalid: empty name
	d = &DAG{Name: "t", Steps: []Step{{
		ID:        "a",
		Command:   "echo",
		Artifacts: []Artifact{{Name: "", Path: "out/data.csv"}},
	}}}
	if err := Validate(d); err == nil {
		t.Error("empty artifact name should fail")
	}

	// Invalid: bad name pattern
	d = &DAG{Name: "t", Steps: []Step{{
		ID:        "a",
		Command:   "echo",
		Artifacts: []Artifact{{Name: "123bad", Path: "out/data.csv"}},
	}}}
	if err := Validate(d); err == nil {
		t.Error("artifact name starting with digit should fail")
	}

	// Invalid: duplicate name
	d = &DAG{Name: "t", Steps: []Step{{
		ID:      "a",
		Command: "echo",
		Artifacts: []Artifact{
			{Name: "data", Path: "out/a.csv"},
			{Name: "data", Path: "out/b.csv"},
		},
	}}}
	if err := Validate(d); err == nil {
		t.Error("duplicate artifact name should fail")
	}

	// Invalid: empty path
	d = &DAG{Name: "t", Steps: []Step{{
		ID:        "a",
		Command:   "echo",
		Artifacts: []Artifact{{Name: "data", Path: ""}},
	}}}
	if err := Validate(d); err == nil {
		t.Error("empty artifact path should fail")
	}
}

func TestValidate_Freshness(t *testing.T) {
	// Valid freshness entries
	d := &DAG{Name: "t", Steps: []Step{{
		ID:      "a",
		Command: "echo",
		Freshness: []FreshnessCheck{
			{Path: "data/raw.csv", MaxAge: "6h"},
			{Path: "data/api.json", MaxAge: "30m", OnStale: "warn"},
			{Path: "data/other.csv", MaxAge: "1h", OnStale: "fail"},
		},
	}}}
	if err := Validate(d); err != nil {
		t.Errorf("valid freshness should pass: %v", err)
	}

	// Invalid: empty path
	d = &DAG{Name: "t", Steps: []Step{{
		ID:        "a",
		Command:   "echo",
		Freshness: []FreshnessCheck{{Path: "", MaxAge: "6h"}},
	}}}
	if err := Validate(d); err == nil {
		t.Error("empty freshness path should fail")
	}

	// Invalid: empty max_age
	d = &DAG{Name: "t", Steps: []Step{{
		ID:        "a",
		Command:   "echo",
		Freshness: []FreshnessCheck{{Path: "data/raw.csv", MaxAge: ""}},
	}}}
	if err := Validate(d); err == nil {
		t.Error("empty freshness max_age should fail")
	}

	// Invalid: bad max_age
	d = &DAG{Name: "t", Steps: []Step{{
		ID:        "a",
		Command:   "echo",
		Freshness: []FreshnessCheck{{Path: "data/raw.csv", MaxAge: "not-a-duration"}},
	}}}
	if err := Validate(d); err == nil {
		t.Error("invalid freshness max_age should fail")
	}

	// Invalid: bad on_stale
	d = &DAG{Name: "t", Steps: []Step{{
		ID:        "a",
		Command:   "echo",
		Freshness: []FreshnessCheck{{Path: "data/raw.csv", MaxAge: "6h", OnStale: "ignore"}},
	}}}
	if err := Validate(d); err == nil {
		t.Error("invalid freshness on_stale should fail")
	}
}

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

func TestValidate_ArtifactAbsolutePath(t *testing.T) {
	d := &DAG{Name: "t", Steps: []Step{{
		ID:        "a",
		Command:   "echo",
		Artifacts: []Artifact{{Name: "data", Path: "/tmp/data.csv"}},
	}}}
	err := Validate(d)
	if err == nil {
		t.Fatal("absolute artifact path should fail validation")
	}
	if !strings.Contains(err.Error(), "must be relative") {
		t.Errorf("error should mention 'must be relative', got: %s", err.Error())
	}
}

func TestValidate_CacheIncompatibleWithApprove(t *testing.T) {
	d := &DAG{Name: "t", Steps: []Step{{
		ID:      "a",
		Approve: &ApproveStep{Message: "ok?"},
		Cache:   true,
	}}}
	err := Validate(d)
	if err == nil {
		t.Fatal("cache + approve should fail validation")
	}
	if !strings.Contains(err.Error(), "incompatible with approve") {
		t.Errorf("error should mention incompatibility, got: %s", err.Error())
	}
}

func TestValidate_CacheIncompatibleWithCall(t *testing.T) {
	d := &DAG{Name: "t", Steps: []Step{{
		ID:    "a",
		Call:  &CallStep{DAG: "other"},
		Cache: true,
	}}}
	err := Validate(d)
	if err == nil {
		t.Fatal("cache + call should fail validation")
	}
	if !strings.Contains(err.Error(), "incompatible with call") {
		t.Errorf("error should mention incompatibility, got: %s", err.Error())
	}
}

func TestValidate_CacheWithScriptIsValid(t *testing.T) {
	d := &DAG{Name: "t", Steps: []Step{{
		ID:     "a",
		Script: "run.R",
		Cache:  true,
	}}}
	if err := Validate(d); err != nil {
		t.Errorf("cache + script should be valid: %v", err)
	}
}
