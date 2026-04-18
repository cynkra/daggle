package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cynkra/daggle/state"
)

func setupAnnotationFixture(t *testing.T) (srv *Server, dag string, runID string) {
	t.Helper()
	dagDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", dataDir)

	dagYAML := `name: demo-ann
steps:
  - id: a
    command: echo a
`
	if err := os.WriteFile(filepath.Join(dagDir, "demo-ann.yaml"), []byte(dagYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	run, err := state.CreateRun("demo-ann")
	if err != nil {
		t.Fatal(err)
	}
	w := state.NewEventWriter(run.Dir)
	_ = w.Write(state.Event{Type: state.EventRunStarted})

	srv = New(func() []state.DAGSource { return []state.DAGSource{{Name: "test", Dir: dagDir}} }, "test-version")
	return srv, "demo-ann", run.ID
}

func TestAnnotations_POSTThenGET(t *testing.T) {
	srv, dag, runID := setupAnnotationFixture(t)

	body := `{"note":"restarted manually","author":"alice"}`
	req := httptest.NewRequest("POST", "/api/v1/dags/"+dag+"/runs/"+runID+"/annotations", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var posted AnnotationEntry
	if err := json.Unmarshal(rr.Body.Bytes(), &posted); err != nil {
		t.Fatalf("unmarshal POST response: %v", err)
	}
	if posted.Note != "restarted manually" || posted.Author != "alice" {
		t.Errorf("POST response = %+v, want note/author echoed", posted)
	}
	if posted.Timestamp == "" {
		t.Error("expected non-empty timestamp in POST response")
	}

	// GET should return a list containing the created annotation.
	getReq := httptest.NewRequest("GET", "/api/v1/dags/"+dag+"/runs/"+runID+"/annotations", nil)
	getRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", getRR.Code, getRR.Body.String())
	}
	var list []AnnotationEntry
	if err := json.Unmarshal(getRR.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal GET: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("annotations list = %+v, want 1 entry", list)
	}
	if list[0].Note != "restarted manually" || list[0].Author != "alice" {
		t.Errorf("list[0] = %+v, want the POSTed annotation", list[0])
	}
}

func TestAnnotations_POST_EmptyNote(t *testing.T) {
	srv, dag, runID := setupAnnotationFixture(t)

	body := `{"note":"   ","author":"alice"}`
	req := httptest.NewRequest("POST", "/api/v1/dags/"+dag+"/runs/"+runID+"/annotations", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rr.Code, rr.Body.String())
	}
}

func TestAnnotations_GET_MissingRun(t *testing.T) {
	srv, dag, _ := setupAnnotationFixture(t)

	req := httptest.NewRequest("GET", "/api/v1/dags/"+dag+"/runs/no-such-id/annotations", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestAnnotations_GET_EmptyList(t *testing.T) {
	srv, dag, runID := setupAnnotationFixture(t)

	req := httptest.NewRequest("GET", "/api/v1/dags/"+dag+"/runs/"+runID+"/annotations", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var list []AnnotationEntry
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if list == nil || len(list) != 0 {
		t.Errorf("empty annotations should serialize as [], got %+v", list)
	}
}
