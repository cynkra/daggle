package dag

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyBaseDefaults(t *testing.T) {
	base := &BaseDefaults{
		Env:       EnvMap{"BASE_VAR": {Value: "base_val"}, "SHARED": {Value: "from_base"}},
		Workdir:   "/base/workdir",
		Timeout:   "5m",
		Retry:     &Retry{Limit: 2},
		OnFailure: &Hook{Command: "echo base failed"},
	}

	d := &DAG{
		Name: "test",
		Env:  EnvMap{"SHARED": {Value: "from_dag"}, "DAG_VAR": {Value: "dag_val"}},
		Steps: []Step{
			{ID: "a", Command: "echo", Timeout: "10m"}, // has own timeout
			{ID: "b", Command: "echo"},                 // inherits base timeout/retry
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
