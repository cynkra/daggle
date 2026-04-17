package tui

import (
	"bytes"
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cynkra/daggle/state"
)

func TestApplyEvent_StepLifecycle(t *testing.T) {
	m := newModel(Config{DAG: "demo", RunID: "r1"}, nil)

	m.applyEvent(state.Event{Type: state.EventRunStarted})
	m.applyEvent(state.Event{Type: state.EventStepStarted, StepID: "a", Attempt: 1})
	m.applyEvent(state.Event{Type: state.EventStepCompleted, StepID: "a", Duration: "100ms", PeakRSSKB: 1024})
	m.applyEvent(state.Event{Type: state.EventStepFailed, StepID: "b", Error: "boom", Duration: "10ms"})
	m.applyEvent(state.Event{Type: state.EventRunFailed})

	if m.status != "failed" {
		t.Errorf("status = %q, want failed", m.status)
	}
	if len(m.steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(m.steps))
	}
	if m.steps[0].Status != "completed" || m.steps[0].PeakRSSKB != 1024 {
		t.Errorf("step a = %+v", m.steps[0])
	}
	if m.steps[1].Status != "failed" || m.steps[1].Error != "boom" {
		t.Errorf("step b = %+v", m.steps[1])
	}
}

func TestView_RendersStepsAndStatus(t *testing.T) {
	m := newModel(Config{DAG: "demo", RunID: "r1"}, nil)
	m.applyEvent(state.Event{Type: state.EventStepStarted, StepID: "extract"})
	m.applyEvent(state.Event{Type: state.EventStepCompleted, StepID: "extract", Duration: "1s"})
	out := stripANSI(m.View())

	for _, want := range []string{"daggle monitor", "demo", "extract", "completed"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q\n---\n%s", want, out)
		}
	}
}

func TestParseSSE_EndpointEventsFlowToChannel(t *testing.T) {
	payload := strings.Join([]string{
		`data: {"type":"run_started","ts":"2026-04-17T00:00:00Z"}`,
		``,
		`data: {"type":"step_started","step_id":"a","ts":"2026-04-17T00:00:01Z"}`,
		``,
		`event: end`,
		`data: {}`,
		``,
	}, "\n")

	ch := make(chan tea.Msg, 8)
	parseSSE(io.NopCloser(bytes.NewBufferString(payload)), ch)
	close(ch)

	var got []tea.Msg
	for msg := range ch {
		got = append(got, msg)
	}
	if len(got) != 3 {
		t.Fatalf("got %d msgs, want 3", len(got))
	}
	if _, ok := got[0].(eventMsg); !ok {
		t.Errorf("msg[0] = %T, want eventMsg", got[0])
	}
	if _, ok := got[1].(eventMsg); !ok {
		t.Errorf("msg[1] = %T, want eventMsg", got[1])
	}
	if _, ok := got[2].(endMsg); !ok {
		t.Errorf("msg[2] = %T, want endMsg", got[2])
	}
}

func TestParseSSE_ErrorEvent(t *testing.T) {
	payload := "event: error\ndata: boom\n\n"
	ch := make(chan tea.Msg, 2)
	parseSSE(io.NopCloser(bytes.NewBufferString(payload)), ch)
	close(ch)
	msg := <-ch
	em, ok := msg.(errMsg)
	if !ok {
		t.Fatalf("msg = %T, want errMsg", msg)
	}
	if em.err == nil || !strings.Contains(em.err.Error(), "boom") {
		t.Errorf("err = %v", em.err)
	}
}

// stripANSI removes lipgloss color escapes for assertion matching.
func stripANSI(s string) string {
	// very small stripper: remove anything between ESC[ and the terminating letter.
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			// skip until a letter terminator
			j := i + 2
			for j < len(s) {
				c := s[j]
				if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
					break
				}
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
