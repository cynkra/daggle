package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCollectWatchPaths_IncludesDAGAndScripts(t *testing.T) {
	dir := t.TempDir()
	dagPath := filepath.Join(dir, "pipeline.yaml")
	script := filepath.Join(dir, "analysis.R")
	validate := filepath.Join(dir, "check.R")

	for _, p := range []string{script, validate} {
		if err := os.WriteFile(p, []byte("# r\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	yaml := "name: t\nsteps:\n" +
		"  - id: run\n    script: analysis.R\n" +
		"  - id: check\n    validate: check.R\n"
	if err := os.WriteFile(dagPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	paths, err := collectWatchPaths(dagPath)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	wantAbs := func(p string) string {
		abs, _ := filepath.Abs(p)
		return abs
	}

	for _, want := range []string{wantAbs(dagPath), wantAbs(script), wantAbs(validate)} {
		if !slices.Contains(paths, want) {
			t.Errorf("expected %s in watched paths, got %v", want, paths)
		}
	}
}

func TestCollectWatchPaths_IncludesBaseYAML(t *testing.T) {
	dir := t.TempDir()
	dagPath := filepath.Join(dir, "pipeline.yaml")
	basePath := filepath.Join(dir, "base.yaml")

	if err := os.WriteFile(basePath, []byte("env: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dagPath, []byte("name: t\nsteps:\n  - id: s\n    command: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	paths, err := collectWatchPaths(dagPath)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	abs, _ := filepath.Abs(basePath)
	if !slices.Contains(paths, abs) {
		t.Errorf("expected base.yaml at %s in watched paths, got %v", abs, paths)
	}
}

func TestCollectWatchPaths_SkipsMissingScripts(t *testing.T) {
	dir := t.TempDir()
	dagPath := filepath.Join(dir, "pipeline.yaml")

	// Script file referenced but not created.
	yaml := "name: t\nsteps:\n  - id: run\n    script: missing.R\n"
	if err := os.WriteFile(dagPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	paths, err := collectWatchPaths(dagPath)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	missingAbs, _ := filepath.Abs(filepath.Join(dir, "missing.R"))
	if slices.Contains(paths, missingAbs) {
		t.Errorf("expected missing script to be skipped, got %v", paths)
	}
}
