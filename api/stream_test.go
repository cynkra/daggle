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

func TestStreamNewLines_DisconnectReturnsAdvancedOffset(t *testing.T) {
	run, _ := state.CreateRun("test-dag")
	w := state.NewEventWriter(run.Dir)
	_ = w.Write(state.Event{Type: state.EventRunStarted})
	_ = w.Write(state.Event{Type: state.EventStepStarted, StepID: "a"})
	_ = w.Write(state.Event{Type: state.EventStepCompleted, StepID: "a"})

	path := run.Dir + "/events.jsonl"

	// Writer that signals disconnect after the second call.
	calls := 0
	fail := func(_, _ string) bool {
		calls++
		return calls < 2
	}

	got, done, err := streamNewLines(path, 0, fail)
	if err != nil {
		t.Fatalf("streamNewLines: %v", err)
	}
	if done {
		t.Error("done should be false on disconnect")
	}
	if got == 0 {
		t.Fatalf("offset should advance past the first line; got %d", got)
	}

	// Subsequent call from `got` offset must emit the remaining events and
	// not re-emit the ones already consumed.
	var seen []string
	ok := func(_, data string) bool {
		seen = append(seen, data)
		return true
	}
	if _, _, err := streamNewLines(path, got, ok); err != nil {
		t.Fatalf("second streamNewLines: %v", err)
	}
	if len(seen) == 0 {
		t.Fatalf("expected to pick up remaining events from advanced offset")
	}
	// None of the remaining events should be the already-written first event
	// (run_started) — disconnect happened after it was written.
	for _, line := range seen {
		if strings.Contains(line, `"run_started"`) {
			t.Errorf("re-emitted run_started after disconnect; line=%s", line)
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
