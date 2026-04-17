package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cynkra/daggle/state"
)

// streamPollInterval is how often the SSE handler checks for new events.
// Exposed for tests to override.
var streamPollInterval = 250 * time.Millisecond

// streamIdleTimeout caps how long the stream remains open after the last
// event if the run never emits a terminal event. Prevents hung connections
// when events.jsonl stops growing mid-run (e.g. the process crashed).
var streamIdleTimeout = 30 * time.Minute

// handleStream tails events.jsonl for a run and pushes each new event as a
// Server-Sent Event. The stream ends when run_completed or run_failed is
// observed, or when the client disconnects.
//
// Query parameters:
//   - from=start (default): replay all events from the beginning, then tail
//   - from=end: skip existing events, only stream new ones
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	runID := r.PathValue("run_id")

	run, err := state.FindRun(name, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	from := r.URL.Query().Get("from")
	path := filepath.Join(run.Dir, "events.jsonl")

	var offset int64
	if from == "end" {
		if fi, err := os.Stat(path); err == nil {
			offset = fi.Size()
		}
	}

	ctx := r.Context()
	ticker := time.NewTicker(streamPollInterval)
	defer ticker.Stop()
	idleDeadline := time.Now().Add(streamIdleTimeout)

	writeSSE := func(event, data string) bool {
		if event != "" {
			if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
				return false
			}
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	for {
		newOffset, done, err := streamNewLines(path, offset, writeSSE)
		if err != nil {
			writeSSE("error", fmt.Sprintf(`{"error":%q}`, err.Error()))
			return
		}
		if newOffset != offset {
			offset = newOffset
			idleDeadline = time.Now().Add(streamIdleTimeout)
		}
		if done {
			writeSSE("end", `{}`)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if time.Now().After(idleDeadline) {
				writeSSE("timeout", `{"reason":"idle"}`)
				return
			}
		}
	}
}

// streamNewLines reads events from path starting at offset, emits each via
// writeSSE, and returns the new offset. The bool return is true when a
// terminal event (run_completed or run_failed) has been seen.
func streamNewLines(path string, offset int64, write func(event, data string) bool) (int64, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return offset, false, nil
		}
		return offset, false, err
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Seek(offset, 0); err != nil {
		return offset, false, err
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var terminal bool
	for scanner.Scan() {
		line := scanner.Text()
		var e state.Event
		if err := json.Unmarshal([]byte(line), &e); err == nil {
			if e.Type == state.EventRunCompleted || e.Type == state.EventRunFailed {
				terminal = true
			}
		}
		if !write("", line) {
			// client disconnected mid-write; the next select{} tick will exit.
			return offset, false, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return offset, false, err
	}

	// Return current file position so the next poll resumes correctly.
	pos, err := f.Seek(0, 1)
	if err != nil {
		return offset, terminal, err
	}
	return pos, terminal, nil
}
