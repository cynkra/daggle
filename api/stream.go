package api

import (
	"bufio"
	"encoding/json"
	"errors"
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

// streamMaxLineBytes is the largest single event line the SSE stream will
// emit. Events larger than this are skipped with a "truncated" marker so
// the stream can recover. 8MB covers any realistic event payload.
const streamMaxLineBytes = 8 * 1024 * 1024

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
	scanner.Buffer(make([]byte, 0, 64*1024), streamMaxLineBytes)
	// Track offset per consumed line rather than reading f.Seek(0, 1) —
	// bufio.Scanner reads ahead in chunks, so the file descriptor's
	// position can overshoot past lines not yet emitted. Assumes LF line
	// endings (events.jsonl is always \n-terminated).
	consumed := offset
	var terminal bool
	for scanner.Scan() {
		line := scanner.Text()
		var e state.Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err == nil {
			if e.Type == state.EventRunCompleted || e.Type == state.EventRunFailed {
				terminal = true
			}
		}
		consumed += int64(len(scanner.Bytes())) + 1 // +1 for the '\n'
		if !write("", line) {
			// Client disconnected mid-write. Return the consumed offset so
			// a retry resumes past the line that was just emitted. The
			// parent loop's <-ctx.Done() path still drives the real exit.
			return consumed, false, nil
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			// One line in events.jsonl is larger than streamMaxLineBytes.
			// Skip past it so the stream can continue, and emit a marker
			// so clients know an event was dropped.
			next, skipErr := skipToNextLine(path, consumed)
			if skipErr != nil {
				return offset, false, skipErr
			}
			skippedBytes := next - consumed - 1 // minus the trailing '\n'
			write("truncated", fmt.Sprintf(`{"bytes":%d}`, skippedBytes))
			return next, false, nil
		}
		return offset, false, err
	}

	return consumed, terminal, nil
}

// skipToNextLine returns the byte offset immediately after the next '\n'
// that follows `start`. If EOF is reached without a newline, the final
// file size is returned.
func skipToNextLine(path string, start int64) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(start, 0); err != nil {
		return 0, err
	}
	r := bufio.NewReader(f)
	pos := start
	for {
		b, err := r.ReadByte()
		if err != nil {
			return pos, nil // treat EOF as end-of-line
		}
		pos++
		if b == '\n' {
			return pos, nil
		}
	}
}
