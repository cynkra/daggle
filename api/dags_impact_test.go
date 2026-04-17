package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cynkra/daggle/state"
)

func TestGetImpact_ExposuresAndDownstream(t *testing.T) {
	dagDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", dataDir)

	// Upstream DAG with one exposure.
	upstream := `name: upstream-etl
description: Nightly ETL
exposures:
  - name: ops-dashboard
    type: dashboard
    url: https://dash.example.com/ops
steps:
  - id: a
    command: echo a
`
	if err := os.WriteFile(filepath.Join(dagDir, "upstream-etl.yaml"), []byte(upstream), 0o644); err != nil {
		t.Fatal(err)
	}

	// Downstream DAG triggered by upstream.
	downstream := `name: downstream-report
trigger:
  on_dag:
    name: upstream-etl
    status: completed
steps:
  - id: render
    command: echo render
`
	if err := os.WriteFile(filepath.Join(dagDir, "downstream-report.yaml"), []byte(downstream), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := New(func() []state.DAGSource { return []state.DAGSource{{Name: "test", Dir: dagDir}} }, "test-version")

	req := httptest.NewRequest("GET", "/api/v1/dags/upstream-etl/impact", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var got ImpactResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "upstream-etl" {
		t.Errorf("name = %q", got.Name)
	}
	if len(got.Exposures) != 1 || got.Exposures[0].Name != "ops-dashboard" {
		t.Errorf("exposures = %+v", got.Exposures)
	}
	if len(got.Downstream) != 1 || got.Downstream[0].Name != "downstream-report" || got.Downstream[0].TriggerOn != "completed" {
		t.Errorf("downstream = %+v", got.Downstream)
	}
}

func TestGetImpact_UnknownDAG(t *testing.T) {
	dagDir := t.TempDir()
	srv := New(func() []state.DAGSource { return []state.DAGSource{{Name: "test", Dir: dagDir}} }, "test-version")

	req := httptest.NewRequest("GET", "/api/v1/dags/nope/impact", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}
