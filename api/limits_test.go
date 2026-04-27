package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cynkra/daggle/state"
)

// TestReadJSON_RejectsOversizeBody confirms the global readJSON limit kicks
// in when a single request body exceeds maxRequestBodyBytes. We hit the
// trigger endpoint because it accepts arbitrary params and is the most
// likely target for an OOM probe.
func TestReadJSON_RejectsOversizeBody(t *testing.T) {
	srv, _ := setupTestServer(t)

	pad := strings.Repeat("a", int(maxRequestBodyBytes)*2)
	body := bytes.NewBufferString(`{"params":{"x":"` + pad + `"}}`)
	req := httptest.NewRequest("POST", "/api/v1/dags/test-dag/run", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	// Accept 400 (decode error from MaxBytesReader truncation) or 413 (if
	// the writer chain signals it). Both mean: server refused to allocate
	// the full body.
	if w.Code == http.StatusCreated {
		t.Fatalf("server accepted oversize body: status=%d body=%s", w.Code, w.Body.String())
	}
	if w.Code != http.StatusBadRequest && w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 400 or 413; body=%s", w.Code, w.Body.String())
	}
}

func TestAddAnnotation_RejectsOversizeNote(t *testing.T) {
	srv, _ := setupTestServer(t)

	// Create a run directly to avoid the async trigger goroutine — that one
	// would race with t.Cleanup unlinking the run dir.
	run, err := state.CreateRun("test-dag")
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	// Note longer than the per-field cap but shorter than the global
	// readJSON cap, so we exercise the per-field check.
	note := strings.Repeat("x", maxAnnotationNoteBytes+1)
	annBody := bytes.NewBufferString(`{"note":"` + note + `","author":"a"}`)
	annReq := httptest.NewRequest("POST",
		"/api/v1/dags/test-dag/runs/"+run.ID+"/annotations", annBody)
	annReq.Header.Set("Content-Type", "application/json")
	annW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(annW, annReq)

	if annW.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", annW.Code, annW.Body.String())
	}
	if !strings.Contains(annW.Body.String(), "maximum length") {
		t.Errorf("expected 'maximum length' in error; got %q", annW.Body.String())
	}
}
