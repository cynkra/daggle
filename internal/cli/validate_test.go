package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateCmd_Valid(t *testing.T) {
	dir := t.TempDir()
	dagFile := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(dagFile, []byte("name: test\nsteps:\n  - id: hello\n    command: echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rootCmd.SetArgs([]string{"validate", dagFile})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected valid DAG to pass, got: %v", err)
	}
}

func TestValidateCmd_Invalid(t *testing.T) {
	dir := t.TempDir()
	dagFile := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(dagFile, []byte("name: bad\nsteps: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rootCmd.SetArgs([]string{"validate", dagFile})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid DAG, got nil")
	}
}

func TestValidateCmd_MissingFile(t *testing.T) {
	rootCmd.SetArgs([]string{"validate", "/nonexistent/dag.yaml"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestValidateCmd_MultiStep(t *testing.T) {
	dir := t.TempDir()
	dagFile := filepath.Join(dir, "multi.yaml")
	content := `name: multi
steps:
  - id: first
    command: echo first
  - id: second
    command: echo second
    depends: [first]
`
	if err := os.WriteFile(dagFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	rootCmd.SetArgs([]string{"validate", dagFile})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected valid multi-step DAG to pass, got: %v", err)
	}
}

func TestValidateCmd_CyclicDependency(t *testing.T) {
	dir := t.TempDir()
	dagFile := filepath.Join(dir, "cyclic.yaml")
	content := `name: cyclic
steps:
  - id: a
    command: echo a
    depends: [b]
  - id: b
    command: echo b
    depends: [a]
`
	if err := os.WriteFile(dagFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	rootCmd.SetArgs([]string{"validate", dagFile})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for cyclic dependency, got nil")
	}
}
