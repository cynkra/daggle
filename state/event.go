package state

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event types
const (
	EventRunStarted         = "run_started"
	EventRunCompleted       = "run_completed"
	EventRunFailed          = "run_failed"
	EventStepStarted        = "step_started"
	EventStepCompleted      = "step_completed"
	EventStepFailed         = "step_failed"
	EventStepRetrying       = "step_retrying"
	EventStepWaitApproval   = "step_waiting_approval"
	EventStepApproved       = "step_approved"
	EventStepRejected       = "step_rejected"
	EventStepArtifact       = "step_artifact"
	EventStepCached         = "step_cached"
	EventRunAnnotated       = "run_annotated"
)

// ArtifactInfo groups artifact-related fields for step_artifact events.
type ArtifactInfo struct {
	ArtifactName    string `json:"artifact_name,omitempty"`
	ArtifactPath    string `json:"artifact_path,omitempty"`     // relative to workdir
	ArtifactAbsPath string `json:"artifact_abs_path,omitempty"` // resolved absolute path
	ArtifactHash    string `json:"artifact_hash,omitempty"`     // SHA-256
	ArtifactSize    int64  `json:"artifact_size,omitempty"`     // bytes
	ArtifactFormat  string `json:"artifact_format,omitempty"`
}

// CacheInfo groups cache-related fields for step_cached events.
type CacheInfo struct {
	CacheKey    string `json:"cache_key,omitempty"`
	CachedRunID string `json:"cached_run_id,omitempty"`
}

// Event represents a lifecycle event in a DAG run.
type Event struct {
	Version     int       `json:"v"`
	Timestamp   time.Time `json:"ts"`
	Type        string    `json:"type"`
	StepID      string    `json:"step_id,omitempty"`
	ExitCode    int       `json:"exit_code,omitempty"`
	Duration    string    `json:"duration,omitempty"`
	Error       string    `json:"error,omitempty"`
	ErrorDetail string    `json:"error_detail,omitempty"`
	Attempt     int       `json:"attempt,omitempty"`
	Message     string    `json:"message,omitempty"`    // approval message
	Approver    string    `json:"approver,omitempty"`   // system user who approved/rejected

	// Annotation fields (used by EventRunAnnotated).
	Note   string `json:"note,omitempty"`
	Author string `json:"author,omitempty"`

	// Resource profiling fields (used by EventStepCompleted).
	PeakRSSKB  int64   `json:"peak_rss_kb,omitempty"`
	UserCPUSec float64 `json:"user_cpu_sec,omitempty"`
	SysCPUSec  float64 `json:"sys_cpu_sec,omitempty"`

	*ArtifactInfo `json:",omitempty"`
	*CacheInfo    `json:",omitempty"`
}

// EventWriter provides thread-safe JSONL event writing.
type EventWriter struct {
	mu     sync.Mutex
	path   string
	runDir string
}

// NewEventWriter creates an EventWriter for the given run directory.
func NewEventWriter(runDir string) *EventWriter {
	return &EventWriter{
		path:   filepath.Join(runDir, "events.jsonl"),
		runDir: runDir,
	}
}

// Write appends an event as a JSON line.
func (w *EventWriter) Write(e Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if e.Version == 0 {
		e.Version = 1
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}

	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open events file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := fmt.Fprintf(f, "%s\n", data); err != nil {
		return err
	}
	// Invalidate the read cache so callers in this process pick up the new
	// event immediately. Cross-process callers rely on mtime/size change.
	InvalidateEventCache(w.runDir)
	return nil
}

// ReadEvents reads all events from a run directory's events.jsonl file.
// The result is cached per runDir and invalidated when the file's mtime
// changes. The SSE streaming endpoint tails events.jsonl with its own
// offset logic and does NOT go through this cache.
func ReadEvents(runDir string) ([]Event, error) {
	path := filepath.Join(runDir, "events.jsonl")
	info, statErr := os.Stat(path)
	if statErr == nil {
		eventCache.mu.Lock()
		cached, ok := eventCache.entries[runDir]
		eventCache.mu.Unlock()
		if ok && cached.mtime.Equal(info.ModTime()) && cached.size == info.Size() {
			return cached.events, nil
		}
	} else if os.IsNotExist(statErr) {
		return nil, statErr
	}

	events, err := readEventsFromFile(path, runDir)
	if err != nil {
		return nil, err
	}

	// Only cache when the stat succeeded. Re-stat after read so we capture
	// the mtime that actually matches the data we parsed; if it changed
	// mid-read, skip caching so a subsequent call re-parses.
	if post, postErr := os.Stat(path); postErr == nil {
		if statErr == nil && post.ModTime().Equal(info.ModTime()) && post.Size() == info.Size() {
			eventCache.mu.Lock()
			eventCache.entries[runDir] = cachedEvents{
				events: events,
				mtime:  post.ModTime(),
				size:   post.Size(),
			}
			eventCache.mu.Unlock()
		}
	}
	return events, nil
}

// InvalidateEventCache drops the cached events for a given runDir. Tests
// that mutate events.jsonl at the same mtime (possible on second-resolution
// filesystems) should call this to force a re-read.
func InvalidateEventCache(runDir string) {
	eventCache.mu.Lock()
	delete(eventCache.entries, runDir)
	eventCache.mu.Unlock()
}

type cachedEvents struct {
	events []Event
	mtime  time.Time
	size   int64
}

var eventCache = struct {
	mu      sync.Mutex
	entries map[string]cachedEvents
}{entries: make(map[string]cachedEvents)}

func readEventsFromFile(path, runDir string) ([]Event, error) {
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
			slog.Warn("skipping malformed event line", "error", err, "dir", runDir)
			continue
		}
		events = append(events, e)
	}
	return events, scanner.Err()
}
