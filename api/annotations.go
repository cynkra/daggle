package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/cynkra/daggle/state"
)

// AnnotationEntry is a single run annotation surfaced via the API.
type AnnotationEntry struct {
	Note      string `json:"note"`
	Author    string `json:"author,omitempty"`
	Timestamp string `json:"timestamp"`
}

// AnnotationRequest is the POST body for adding an annotation.
type AnnotationRequest struct {
	Note   string `json:"note"`
	Author string `json:"author,omitempty"`
}

func (s *Server) handleListAnnotations(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	runID := r.PathValue("run_id")

	run, err := state.FindRun(name, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	events, err := state.ReadEvents(run.Dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read events: "+err.Error())
		return
	}

	var out []AnnotationEntry
	for _, e := range events {
		if e.Type != state.EventRunAnnotated {
			continue
		}
		out = append(out, AnnotationEntry{
			Note:      e.Note,
			Author:    e.Author,
			Timestamp: formatTime(e.Timestamp),
		})
	}
	if out == nil {
		out = []AnnotationEntry{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAddAnnotation(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	runID := r.PathValue("run_id")

	var req AnnotationRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	req.Note = strings.TrimSpace(req.Note)
	if req.Note == "" {
		writeError(w, http.StatusBadRequest, "note is required")
		return
	}

	run, err := state.FindRun(name, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	ew := state.NewEventWriter(run.Dir)
	defer func() { _ = ew.Close() }()
	if err := ew.Write(state.Event{
		Type:   state.EventRunAnnotated,
		Note:   req.Note,
		Author: req.Author,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "write annotation: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, AnnotationEntry{
		Note:      req.Note,
		Author:    req.Author,
		Timestamp: formatTime(time.Now()),
	})
}
