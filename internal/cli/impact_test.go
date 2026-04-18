package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupDAGsDir creates a tempdir + <tempdir>/dags, points DAGGLE_CONFIG_DIR
// at it so state.BuildDAGSources picks it up, and returns the dags dir path.
func setupDAGsDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dags := filepath.Join(root, "dags")
	if err := os.MkdirAll(dags, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DAGGLE_CONFIG_DIR", root)
	return dags
}

func TestImpactCmd_ListsDownstreamAndExposures(t *testing.T) {
	dags := setupDAGsDir(t)
	if err := os.WriteFile(filepath.Join(dags, "upstream.yaml"), []byte(`name: upstream
steps:
  - id: a
    command: echo hi
exposures:
  - name: ops-dashboard
    type: dashboard
    url: https://dash.example.com/ops
    description: Main ops dashboard
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dags, "downstream.yaml"), []byte(`name: downstream
steps:
  - id: b
    command: echo hi
trigger:
  on_dag:
    name: upstream
    status: completed
`), 0o644); err != nil {
		t.Fatal(err)
	}

	rootCmd.SetArgs([]string{"impact", "upstream"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("impact returned error: %v", err)
	}
}

func TestImpactCmd_UnknownDAG(t *testing.T) {
	_ = setupDAGsDir(t)

	rootCmd.SetArgs([]string{"impact", "no-such"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown DAG")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want contains 'not found'", err)
	}
}

func TestImpactCmd_NoDownstream(t *testing.T) {
	dags := setupDAGsDir(t)
	if err := os.WriteFile(filepath.Join(dags, "solo.yaml"), []byte(`name: solo
steps:
  - id: a
    command: echo hi
`), 0o644); err != nil {
		t.Fatal(err)
	}

	rootCmd.SetArgs([]string{"impact", "solo"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("impact on isolated DAG should succeed: %v", err)
	}
}
