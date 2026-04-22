package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnifiedDiff_Identical(t *testing.T) {
	got := unifiedDiff([]string{"a", "b", "c"}, []string{"a", "b", "c"}, "x", "y")
	if got != "" {
		t.Errorf("identical inputs should produce empty diff, got: %q", got)
	}
}

func TestUnifiedDiff_AddLine(t *testing.T) {
	got := unifiedDiff([]string{"a", "b"}, []string{"a", "b", "c"}, "old", "new")
	if !strings.Contains(got, "+c") {
		t.Errorf("diff missing '+c':\n%s", got)
	}
	if !strings.HasPrefix(got, "--- old\n+++ new\n") {
		t.Errorf("diff has wrong header:\n%s", got)
	}
}

func TestUnifiedDiff_RemoveLine(t *testing.T) {
	got := unifiedDiff([]string{"a", "b", "c"}, []string{"a", "c"}, "x", "y")
	if !strings.Contains(got, "-b") {
		t.Errorf("diff missing '-b':\n%s", got)
	}
}

func TestWriteDAGDiff_NoPrior(t *testing.T) {
	dir := t.TempDir()
	wrote, err := WriteDAGDiff(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Error("expected no diff written when priorRun is nil")
	}
}

func TestWriteDAGDiff_PriorMissingSnapshot(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", dataDir)

	prior, err := CreateRun("demo")
	if err != nil {
		t.Fatal(err)
	}
	current, err := CreateRun("demo")
	if err != nil {
		t.Fatal(err)
	}
	// Write current dag.yaml only — prior has none (pre-Phase 9 run).
	if err := os.WriteFile(filepath.Join(current.Dir, DAGYAMLName), []byte("name: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wrote, err := WriteDAGDiff(current.Dir, prior)
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Error("expected no diff when prior snapshot is missing")
	}
}

func TestWriteDAGDiff_Identical(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", dataDir)

	prior, err := CreateRun("demo")
	if err != nil {
		t.Fatal(err)
	}
	current, err := CreateRun("demo")
	if err != nil {
		t.Fatal(err)
	}
	yaml := "name: demo\nsteps:\n  - id: a\n    command: echo\n"
	if err := os.WriteFile(filepath.Join(prior.Dir, DAGYAMLName), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current.Dir, DAGYAMLName), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	wrote, err := WriteDAGDiff(current.Dir, prior)
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Error("expected no diff for identical YAML")
	}
	if _, err := os.Stat(filepath.Join(current.Dir, DAGDiffName)); !os.IsNotExist(err) {
		t.Errorf("dag_diff.patch should not exist, got err=%v", err)
	}
}

func TestWriteDAGDiff_Changed(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", dataDir)

	prior, err := CreateRun("demo")
	if err != nil {
		t.Fatal(err)
	}
	current, err := CreateRun("demo")
	if err != nil {
		t.Fatal(err)
	}
	priorYAML := "name: demo\nsteps:\n  - id: a\n    command: echo a\n"
	currentYAML := "name: demo\nsteps:\n  - id: a\n    command: echo a\n  - id: b\n    command: echo b\n"
	if err := os.WriteFile(filepath.Join(prior.Dir, DAGYAMLName), []byte(priorYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current.Dir, DAGYAMLName), []byte(currentYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	wrote, err := WriteDAGDiff(current.Dir, prior)
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatal("expected diff to be written for changed YAML")
	}

	patch, err := os.ReadFile(filepath.Join(current.Dir, DAGDiffName))
	if err != nil {
		t.Fatalf("read patch: %v", err)
	}
	patchStr := string(patch)
	if !strings.Contains(patchStr, "+  - id: b") {
		t.Errorf("expected '+  - id: b' line in diff, got:\n%s", patchStr)
	}
	if !strings.HasPrefix(patchStr, "--- ") {
		t.Errorf("diff missing unified-diff header:\n%s", patchStr)
	}
}
