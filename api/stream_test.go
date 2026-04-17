package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cynkra/daggle/state"
)

func TestStream_ExistingEventsThenEnd(t *testing.T) {
	srv, _ := setupTestServer(t)

	// Create a run with some events including a terminal one.
	run, _ := state.CreateRun("test-dag")
	w := state.NewEventWriter(run.Dir)
	_ = w.Write(state.Event{Type: state.EventRunStarted})
	_ = w.Write(state.Event{Type: state.EventStepStarted, StepID: "build"})
	_ = w.Write(state.Event{Type: state.EventStepCompleted, StepID: "build", Duration: "100ms"})
	_ = w.Write(state.Event{Type: state.EventRunCompleted})

	req := httptest.NewRequest("GET", "/api/v1/dags/test-dag/runs/"+run.ID+"/stream", nil)
	rr := httptest.NewRecorder()

	// Short poll interval so the test runs quickly.
	oldInterval := streamPollInterval
	streamPollInterval = 20 * time.Millisecond
	t.Cleanup(func() { streamPollInterval = oldInterval })

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rr.Body.String()

	for _, want := range []string{
		`"run_started"`,
		`"step_started"`,
		`"step_completed"`,
		`"run_completed"`,
		"event: end",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n---\n%s", want, body)
		}
	}
}

func TestStream_UnknownRun(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/dags/test-dag/runs/no-such-id/stream", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}
