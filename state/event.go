package state

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event types
const (
	EventRunStarted   = "run_started"
	EventRunCompleted = "run_completed"
	EventRunFailed    = "run_failed"
	EventStepStarted  = "step_started"
	EventStepCompleted = "step_completed"
	EventStepFailed   = "step_failed"
	EventStepRetrying = "step_retrying"
)

// Event represents a lifecycle event in a DAG run.
type Event struct {
	Timestamp   time.Time `json:"ts"`
	Type        string    `json:"type"`
	StepID      string    `json:"step_id,omitempty"`
	ExitCode    int       `json:"exit_code,omitempty"`
	Duration    string    `json:"duration,omitempty"`
	Error       string    `json:"error,omitempty"`
	ErrorDetail string    `json:"error_detail,omitempty"`
	Attempt     int       `json:"attempt,omitempty"`
}

// EventWriter provides thread-safe JSONL event writing.
type EventWriter struct {
	mu   sync.Mutex
	path string
}

// NewEventWriter creates an EventWriter for the given run directory.
func NewEventWriter(runDir string) *EventWriter {
	return &EventWriter{
		path: filepath.Join(runDir, "events.jsonl"),
	}
}

// Write appends an event as a JSON line.
func (w *EventWriter) Write(e Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}

	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open events file: %w", err)
	}
	defer func() { _ = f.Close() }()

	_, err = fmt.Fprintf(f, "%s\n", data)
	return err
}

// ReadEvents reads all events from a run directory's events.jsonl file.
func ReadEvents(runDir string) ([]Event, error) {
	path := filepath.Join(runDir, "events.jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var events []Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue // skip malformed lines
		}
		events = append(events, e)
	}
	return events, scanner.Err()
}
