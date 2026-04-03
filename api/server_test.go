package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cynkra/daggle/state"
)

func setupTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dagDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", dataDir)

	// Write a test DAG
	dagYAML := `name: test-dag
trigger:
  schedule: "@every 1h"
steps:
  - id: extract
    command: echo extracting
  - id: validate
    command: echo validating
    depends: [extract]
`
	if err := os.WriteFile(filepath.Join(dagDir, "test-dag.yaml"), []byte(dagYAML), 0644); err != nil {
		t.Fatal(err)
	}

	srv := New(dagDir, "test-version")
	return srv, dagDir
}

func TestHealth(t *testing.T) {
	srv, _ := setupTestServer(t)
	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("status = %q, want %q", resp.Status, "ok")
	}
	if resp.Version != "test-version" {
		t.Errorf("version = %q, want %q", resp.Version, "test-version")
	}
}

func TestListDAGs(t *testing.T) {
	srv, _ := setupTestServer(t)
	req := httptest.NewRequest("GET", "/api/v1/dags", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var dags []DAGSummary
	if err := json.Unmarshal(w.Body.Bytes(), &dags); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(dags) != 1 {
		t.Fatalf("dags = %d, want 1", len(dags))
	}
	if dags[0].Name != "test-dag" {
		t.Errorf("name = %q, want %q", dags[0].Name, "test-dag")
	}
	if dags[0].Steps != 2 {
		t.Errorf("steps = %d, want 2", dags[0].Steps)
	}
	if dags[0].Schedule != "@every 1h" {
		t.Errorf("schedule = %q, want %q", dags[0].Schedule, "@every 1h")
	}
}

func TestGetDAG(t *testing.T) {
	srv, _ := setupTestServer(t)
	req := httptest.NewRequest("GET", "/api/v1/dags/test-dag", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var detail DAGDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if detail.Name != "test-dag" {
		t.Errorf("name = %q", detail.Name)
	}
	if len(detail.StepIDs) != 2 {
		t.Errorf("step_ids = %v, want 2 entries", detail.StepIDs)
	}
}

func TestGetDAG_NotFound(t *testing.T) {
	srv, _ := setupTestServer(t)
	req := httptest.NewRequest("GET", "/api/v1/dags/nonexistent", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestListRuns_Empty(t *testing.T) {
	srv, _ := setupTestServer(t)
	req := httptest.NewRequest("GET", "/api/v1/dags/test-dag/runs", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var runs []RunSummary
	if err := json.Unmarshal(w.Body.Bytes(), &runs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("runs = %d, want 0", len(runs))
	}
}

func TestGetRun_WithData(t *testing.T) {
	srv, _ := setupTestServer(t)

	// Create a run manually
	run, err := state.CreateRun("test-dag")
	if err != nil {
		t.Fatal(err)
	}

	// Write some events
	writer := state.NewEventWriter(run.Dir)
	_ = writer.Write(state.Event{Type: state.EventRunStarted})
	_ = writer.Write(state.Event{Type: state.EventStepStarted, StepID: "extract", Attempt: 1})
	_ = writer.Write(state.Event{Type: state.EventStepCompleted, StepID: "extract", Duration: "1.5s", Attempt: 1})
	_ = writer.Write(state.Event{Type: state.EventRunCompleted})

	// Write meta
	_ = state.WriteMeta(run.Dir, &state.RunMeta{
		RunID:   run.ID,
		DAGName: "test-dag",
		DAGHash: "abcdef123456789012345678901234567890",
	})

	req := httptest.NewRequest("GET", "/api/v1/dags/test-dag/runs/"+run.ID, nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var detail RunDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if detail.RunID != run.ID {
		t.Errorf("run_id = %q", detail.RunID)
	}
	if detail.Status != "completed" {
		t.Errorf("status = %q, want completed", detail.Status)
	}
	if len(detail.Steps) != 1 {
		t.Errorf("steps = %d, want 1", len(detail.Steps))
	}
	if detail.DAGHash != "abcdef123456" {
		t.Errorf("dag_hash = %q, want first 12 chars", detail.DAGHash)
	}
}

func TestGetRun_Latest(t *testing.T) {
	srv, _ := setupTestServer(t)

	run, _ := state.CreateRun("test-dag")
	writer := state.NewEventWriter(run.Dir)
	_ = writer.Write(state.Event{Type: state.EventRunStarted})
	_ = writer.Write(state.Event{Type: state.EventRunCompleted})

	req := httptest.NewRequest("GET", "/api/v1/dags/test-dag/runs/latest", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var detail RunDetail
	_ = json.Unmarshal(w.Body.Bytes(), &detail)
	if detail.RunID != run.ID {
		t.Errorf("run_id = %q, want %q (latest)", detail.RunID, run.ID)
	}
}

func TestStepLog(t *testing.T) {
	srv, _ := setupTestServer(t)

	run, _ := state.CreateRun("test-dag")
	_ = os.WriteFile(filepath.Join(run.Dir, "extract.stdout.log"), []byte("Loading data...\n"), 0644)
	_ = os.WriteFile(filepath.Join(run.Dir, "extract.stderr.log"), []byte(""), 0644)

	writer := state.NewEventWriter(run.Dir)
	_ = writer.Write(state.Event{Type: state.EventRunStarted})

	req := httptest.NewRequest("GET", "/api/v1/dags/test-dag/runs/"+run.ID+"/steps/extract/log", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var log StepLog
	_ = json.Unmarshal(w.Body.Bytes(), &log)
	if log.Stdout != "Loading data...\n" {
		t.Errorf("stdout = %q", log.Stdout)
	}
}

func TestOutputs(t *testing.T) {
	srv, _ := setupTestServer(t)

	run, _ := state.CreateRun("test-dag")
	// Write stdout with output markers
	_ = os.WriteFile(filepath.Join(run.Dir, "extract.stdout.log"),
		[]byte("some output\n::daggle-output name=row_count::42\nnormal line\n::daggle-output name=file_path::/tmp/data.csv\n"), 0644)

	writer := state.NewEventWriter(run.Dir)
	_ = writer.Write(state.Event{Type: state.EventStepStarted, StepID: "extract"})
	_ = writer.Write(state.Event{Type: state.EventStepCompleted, StepID: "extract"})

	req := httptest.NewRequest("GET", "/api/v1/dags/test-dag/runs/"+run.ID+"/outputs", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var outputs []OutputEntry
	_ = json.Unmarshal(w.Body.Bytes(), &outputs)
	if len(outputs) != 2 {
		t.Fatalf("outputs = %d, want 2", len(outputs))
	}

	// Check that outputs are flat and R-friendly
	found := make(map[string]string)
	for _, o := range outputs {
		found[o.Key] = o.Value
		if o.StepID != "extract" {
			t.Errorf("step_id = %q, want extract", o.StepID)
		}
	}
	if found["row_count"] != "42" {
		t.Errorf("row_count = %q", found["row_count"])
	}
	if found["file_path"] != "/tmp/data.csv" {
		t.Errorf("file_path = %q", found["file_path"])
	}
}

func TestTriggerRun(t *testing.T) {
	srv, _ := setupTestServer(t)

	body := bytes.NewBufferString(`{"params": {"dept": "sales"}}`)
	req := httptest.NewRequest("POST", "/api/v1/dags/test-dag/run", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var resp TriggerResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.RunID == "" {
		t.Error("run_id should not be empty")
	}
	if resp.Status != "started" {
		t.Errorf("status = %q, want started", resp.Status)
	}

	// Give the async goroutine a moment to start before cleanup
	time.Sleep(200 * time.Millisecond)
}

func TestCancelRun_NotRunning(t *testing.T) {
	srv, _ := setupTestServer(t)

	run, _ := state.CreateRun("test-dag")
	writer := state.NewEventWriter(run.Dir)
	_ = writer.Write(state.Event{Type: state.EventRunStarted})
	_ = writer.Write(state.Event{Type: state.EventRunCompleted})

	req := httptest.NewRequest("POST", "/api/v1/dags/test-dag/runs/"+run.ID+"/cancel", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d (already completed)", w.Code, http.StatusConflict)
	}
}
