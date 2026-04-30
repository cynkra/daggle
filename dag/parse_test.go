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
	// "name is required" is intentionally absent: ParseFile fills an empty
	// name from the file basename. The remaining errors must still surface.
	for _, want := range []string{"must have one of", "multiple types", "invalid timeout", "unknown step"} {
		if !strings.Contains(errMsg, want) {
			t.Errorf("error missing %q, got: %s", want, errMsg)
		}
	}
}

func TestParseFile_FillsNameFromFilename(t *testing.T) {
	dir := t.TempDir()
	content := []byte("steps:\n  - id: a\n    command: echo\n")
	dagPath := filepath.Join(dir, "no-name.yaml")
	if err := os.WriteFile(dagPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := ParseFile(dagPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if d.Name != "no-name" {
		t.Errorf("Name = %q, want %q (filled from filename)", d.Name, "no-name")
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
