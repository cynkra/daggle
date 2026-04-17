package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	if err := os.WriteFile(filepath.Join(dagDir, "test-dag.yaml"), []byte(dagYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := New(func() []state.DAGSource { return []state.DAGSource{{Name: "test", Dir: dagDir}} }, "test-version")
	return srv, dagDir
}

func TestOpenAPISpec(t *testing.T) {
	srv, _ := setupTestServer(t)
	req := httptest.NewRequest("GET", "/openapi.yaml", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "text/yaml; charset=utf-8" {
		t.Errorf("content-type = %q, want text/yaml; charset=utf-8", ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, "openapi:") {
		t.Error("response does not contain openapi: key")
	}
	if !strings.Contains(body, "/api/v1/health") {
		t.Error("response does not contain /api/v1/health path")
	}
	if !strings.Contains(body, "/api/v1/projects") {
		t.Error("response does not contain /api/v1/projects path")
	}
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
	_ = os.WriteFile(filepath.Join(run.Dir, "extract.stdout.log"), []byte("Loading data...\n"), 0o644)
	_ = os.WriteFile(filepath.Join(run.Dir, "extract.stderr.log"), []byte(""), 0o644)

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
		[]byte("some output\n::daggle-output name=row_count::42\nnormal line\n::daggle-output name=file_path::/tmp/data.csv\n"), 0o644)

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

func TestListProjects(t *testing.T) {
	srv, _ := setupTestServer(t)
	configDir := t.TempDir()
	t.Setenv("DAGGLE_CONFIG_DIR", configDir)

	req := httptest.NewRequest("GET", "/api/v1/projects", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var projects []ProjectSummary
	if err := json.Unmarshal(w.Body.Bytes(), &projects); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Should always have the global entry
	if len(projects) < 1 {
		t.Fatal("expected at least the global entry")
	}
	if projects[0].Name != "(global)" {
		t.Errorf("first entry name = %q, want (global)", projects[0].Name)
	}
}

func TestRegisterProject(t *testing.T) {
	srv, _ := setupTestServer(t)
	configDir := t.TempDir()
	t.Setenv("DAGGLE_CONFIG_DIR", configDir)

	// Create a project directory with a .daggle/ dir containing a DAG
	projDir := t.TempDir()
	dagDir := filepath.Join(projDir, ".daggle")
	if err := os.MkdirAll(dagDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dagDir, "my-dag.yaml"), []byte("name: my-dag\nsteps:\n  - id: a\n    command: echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	body := bytes.NewBufferString(`{"name": "my-project", "path": "` + projDir + `"}`)
	req := httptest.NewRequest("POST", "/api/v1/projects", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var resp RegisterResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Name != "my-project" {
		t.Errorf("name = %q, want my-project", resp.Name)
	}

	// Verify it appears in the project list
	req = httptest.NewRequest("GET", "/api/v1/projects", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	var projects []ProjectSummary
	_ = json.Unmarshal(w.Body.Bytes(), &projects)
	if len(projects) != 2 { // global + my-project
		t.Fatalf("projects = %d, want 2", len(projects))
	}
	if projects[1].Name != "my-project" {
		t.Errorf("project name = %q, want my-project", projects[1].Name)
	}
	if projects[1].DAGs != 1 {
		t.Errorf("dags = %d, want 1", projects[1].DAGs)
	}
	if projects[1].Status != "ok" {
		t.Errorf("status = %q, want ok", projects[1].Status)
	}
}

func TestRegisterProject_Duplicate(t *testing.T) {
	srv, _ := setupTestServer(t)
	configDir := t.TempDir()
	t.Setenv("DAGGLE_CONFIG_DIR", configDir)

	projDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projDir, ".daggle"), 0o755); err != nil {
		t.Fatal(err)
	}

	body := bytes.NewBufferString(`{"name": "dup", "path": "` + projDir + `"}`)
	req := httptest.NewRequest("POST", "/api/v1/projects", body)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("first register: status = %d", w.Code)
	}

	// Register same name again with different path
	projDir2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projDir2, ".daggle"), 0o755); err != nil {
		t.Fatal(err)
	}
	body = bytes.NewBufferString(`{"name": "dup", "path": "` + projDir2 + `"}`)
	req = httptest.NewRequest("POST", "/api/v1/projects", body)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("duplicate register: status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestRegisterProject_MissingPath(t *testing.T) {
	srv, _ := setupTestServer(t)

	body := bytes.NewBufferString(`{"name": "no-path"}`)
	req := httptest.NewRequest("POST", "/api/v1/projects", body)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestUnregisterProject(t *testing.T) {
	srv, _ := setupTestServer(t)
	configDir := t.TempDir()
	t.Setenv("DAGGLE_CONFIG_DIR", configDir)

	projDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projDir, ".daggle"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Register first
	body := bytes.NewBufferString(`{"name": "to-remove", "path": "` + projDir + `"}`)
	req := httptest.NewRequest("POST", "/api/v1/projects", body)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("register: status = %d", w.Code)
	}

	// Unregister
	req = httptest.NewRequest("DELETE", "/api/v1/projects/to-remove", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unregister: status = %d, body = %s", w.Code, w.Body.String())
	}

	var resp UnregisterResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Name != "to-remove" {
		t.Errorf("name = %q, want to-remove", resp.Name)
	}

	// Verify it's gone from the list
	req = httptest.NewRequest("GET", "/api/v1/projects", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	var projects []ProjectSummary
	_ = json.Unmarshal(w.Body.Bytes(), &projects)
	if len(projects) != 1 { // only global remains
		t.Errorf("projects = %d, want 1 (global only)", len(projects))
	}
}

func TestUnregisterProject_NotFound(t *testing.T) {
	srv, _ := setupTestServer(t)
	configDir := t.TempDir()
	t.Setenv("DAGGLE_CONFIG_DIR", configDir)

	req := httptest.NewRequest("DELETE", "/api/v1/projects/nonexistent", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
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

func TestAddAndListAnnotations(t *testing.T) {
	srv, _ := setupTestServer(t)

	run, _ := state.CreateRun("test-dag")
	writer := state.NewEventWriter(run.Dir)
	_ = writer.Write(state.Event{Type: state.EventRunStarted})
	_ = writer.Write(state.Event{Type: state.EventRunCompleted})

	// POST an annotation
	body := bytes.NewBufferString(`{"note":"db was down","author":"alice"}`)
	req := httptest.NewRequest("POST", "/api/v1/dags/test-dag/runs/"+run.ID+"/annotations", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, body = %s", w.Code, w.Body.String())
	}

	// GET annotations
	req = httptest.NewRequest("GET", "/api/v1/dags/test-dag/runs/"+run.ID+"/annotations", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", w.Code, w.Body.String())
	}
	var entries []AnnotationEntry
	if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d annotations, want 1", len(entries))
	}
	if entries[0].Note != "db was down" {
		t.Errorf("note = %q, want %q", entries[0].Note, "db was down")
	}
	if entries[0].Author != "alice" {
		t.Errorf("author = %q, want alice", entries[0].Author)
	}
}

func TestAddAnnotation_EmptyNote(t *testing.T) {
	srv, _ := setupTestServer(t)

	run, _ := state.CreateRun("test-dag")
	writer := state.NewEventWriter(run.Dir)
	_ = writer.Write(state.Event{Type: state.EventRunStarted})

	body := bytes.NewBufferString(`{"note":""}`)
	req := httptest.NewRequest("POST", "/api/v1/dags/test-dag/runs/"+run.ID+"/annotations", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
