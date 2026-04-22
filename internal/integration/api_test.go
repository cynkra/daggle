//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cynkra/daggle/api"
	"github.com/cynkra/daggle/state"
)

// TestAPIOverRealRun wires the API server to a DAG source directory,
// runs a real DAG, and verifies the API reflects what the engine
// actually produced (events, steps, logs).
func TestAPIOverRealRun(t *testing.T) {
	isolate(t)

	// Copy the linear fixture into a temp DAG source dir so the API's
	// dagPath resolver can find it by name.
	dagDir := t.TempDir()
	src, err := os.ReadFile(fixturePath(t, "linear.yaml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dagDir, "integration-linear.yaml"), src, 0o644); err != nil {
		t.Fatalf("write dag: %v", err)
	}

	d := loadDAG(t, "linear.yaml", nil)
	run, err := runDAG(t, d)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	srv := api.New(func() []state.DAGSource {
		return []state.DAGSource{{Name: "test", Dir: dagDir}}
	}, "integration")

	// --- GET /api/v1/dags ---
	req := httptest.NewRequest("GET", "/api/v1/dags", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list dags: status = %d, body = %s", w.Code, w.Body.String())
	}

	// --- GET /api/v1/dags/{name}/runs ---
	req = httptest.NewRequest("GET", "/api/v1/dags/integration-linear/runs", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list runs: status = %d, body = %s", w.Code, w.Body.String())
	}
	var runs []struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &runs); err != nil {
		t.Fatalf("unmarshal runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	if runs[0].RunID != run.ID {
		t.Errorf("run_id = %q, want %q", runs[0].RunID, run.ID)
	}
	if runs[0].Status != "completed" {
		t.Errorf("status = %q, want completed", runs[0].Status)
	}

	// --- GET /api/v1/dags/{name}/runs/{run_id} ---
	req = httptest.NewRequest("GET", "/api/v1/dags/integration-linear/runs/"+run.ID, nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get run: status = %d, body = %s", w.Code, w.Body.String())
	}
	var detail struct {
		Status string `json:"status"`
		Steps  []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if detail.Status != "completed" {
		t.Errorf("status = %q, want completed", detail.Status)
	}
	if len(detail.Steps) != 3 {
		t.Errorf("steps = %d, want 3", len(detail.Steps))
	}

	// --- GET /api/v1/dags/{name}/runs/{run_id}/steps/{step_id}/log ---
	req = httptest.NewRequest("GET", "/api/v1/dags/integration-linear/runs/"+run.ID+"/steps/a/log", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("step log: status = %d, body = %s", w.Code, w.Body.String())
	}
	var log struct {
		Stdout string `json:"stdout"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &log); err != nil {
		t.Fatalf("unmarshal log: %v", err)
	}
	if log.Stdout == "" {
		t.Errorf("stdout is empty; expected echo output from step 'a'")
	}
}
