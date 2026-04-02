package state

import (
	"os"
	"testing"
)

func TestCreateRun(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	run, err := CreateRun("test-dag")
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	if run.ID == "" {
		t.Error("run ID is empty")
	}
	if run.DAGName != "test-dag" {
		t.Errorf("DAGName = %q, want %q", run.DAGName, "test-dag")
	}
	if _, err := os.Stat(run.Dir); os.IsNotExist(err) {
		t.Errorf("run dir does not exist: %s", run.Dir)
	}
}

func TestEventRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	writer := NewEventWriter(tmpDir)

	events := []Event{
		{Type: EventRunStarted},
		{Type: EventStepStarted, StepID: "extract"},
		{Type: EventStepCompleted, StepID: "extract", Duration: "1.5s"},
		{Type: EventRunCompleted},
	}

	for _, e := range events {
		if err := writer.Write(e); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	read, err := ReadEvents(tmpDir)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}

	if len(read) != len(events) {
		t.Fatalf("read %d events, want %d", len(read), len(events))
	}

	for i, e := range read {
		if e.Type != events[i].Type {
			t.Errorf("event %d type = %q, want %q", i, e.Type, events[i].Type)
		}
	}
}

func TestListRuns(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	// Create a few runs
	for i := 0; i < 3; i++ {
		_, err := CreateRun("test-dag")
		if err != nil {
			t.Fatalf("CreateRun %d: %v", i, err)
		}
	}

	runs, err := ListRuns("test-dag")
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 3 {
		t.Errorf("runs = %d, want 3", len(runs))
	}

	// Should be sorted descending by time
	for i := 1; i < len(runs); i++ {
		if runs[i].StartTime.After(runs[i-1].StartTime) {
			t.Errorf("run %d (%v) is after run %d (%v)", i, runs[i].StartTime, i-1, runs[i-1].StartTime)
		}
	}
}

func TestRunStatus(t *testing.T) {
	tmpDir := t.TempDir()
	writer := NewEventWriter(tmpDir)

	if s := RunStatus(tmpDir); s != "unknown" {
		t.Errorf("empty status = %q, want %q", s, "unknown")
	}

	_ = writer.Write(Event{Type: EventRunStarted})
	if s := RunStatus(tmpDir); s != "running" {
		t.Errorf("after start = %q, want %q", s, "running")
	}

	_ = writer.Write(Event{Type: EventRunCompleted})
	if s := RunStatus(tmpDir); s != "completed" {
		t.Errorf("after complete = %q, want %q", s, "completed")
	}
}

func TestMetaRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()

	meta := &RunMeta{
		RunID:         "abc123",
		DAGName:       "test-dag",
		DAGHash:       "sha256hash",
		Platform:      "darwin/arm64",
		DaggleVersion: "0.1.0",
		RVersion:      "4.4.1",
	}

	if err := WriteMeta(tmpDir, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	read, err := ReadMeta(tmpDir)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}

	if read.RunID != "abc123" {
		t.Errorf("RunID = %q, want %q", read.RunID, "abc123")
	}
	if read.DAGHash != "sha256hash" {
		t.Errorf("DAGHash = %q, want %q", read.DAGHash, "sha256hash")
	}
	if read.RVersion != "4.4.1" {
		t.Errorf("RVersion = %q, want %q", read.RVersion, "4.4.1")
	}
}

func TestXDGPaths(t *testing.T) {
	t.Setenv("DAGGLE_CONFIG_DIR", "/custom/config")
	t.Setenv("DAGGLE_DATA_DIR", "/custom/data")

	if got := ConfigDir(); got != "/custom/config" {
		t.Errorf("ConfigDir = %q, want %q", got, "/custom/config")
	}
	if got := DataDir(); got != "/custom/data" {
		t.Errorf("DataDir = %q, want %q", got, "/custom/data")
	}
}
