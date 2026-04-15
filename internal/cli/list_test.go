package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListCmd_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	dagsDir = dir
	t.Cleanup(func() { dagsDir = "" })

	rootCmd.SetArgs([]string{"list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestListCmd_WithDags(t *testing.T) {
	dir := t.TempDir()
	dagFile := filepath.Join(dir, "my-dag.yaml")
	if err := os.WriteFile(dagFile, []byte("name: my-dag\nsteps:\n  - id: a\n    command: echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dagsDir = dir
	t.Cleanup(func() { dagsDir = "" })

	rootCmd.SetArgs([]string{"list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestListCmd_InvalidDag(t *testing.T) {
	dir := t.TempDir()
	dagFile := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(dagFile, []byte("not valid yaml: ["), 0o644); err != nil {
		t.Fatal(err)
	}
	dagsDir = dir
	t.Cleanup(func() { dagsDir = "" })

	// Should not error - invalid DAGs are listed as INVALID
	rootCmd.SetArgs([]string{"list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
}
