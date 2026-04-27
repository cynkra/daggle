package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/cynkra/daggle/scheduler"
)

// ScheduleManager is the subset of *scheduler.Scheduler the API needs. A
// narrow interface keeps the handlers testable and breaks the hard link to
// the concrete scheduler type.
type ScheduleManager interface {
	ListSchedules(dagName string) []scheduler.ScheduleView
	AddRuntimeSchedule(dagName string, cronExpr string, params map[string]string, enabled bool) (scheduler.ScheduleView, error)
	RemoveRuntimeSchedule(dagName, scheduleID string) error
	SetRuntimeScheduleEnabled(dagName, scheduleID string, enabled bool) (scheduler.ScheduleView, error)
}

// WithScheduleManager wires a ScheduleManager into the server. When absent,
// all /schedules endpoints return 503 so API-only deployments (no scheduler
// process running) surface a clear error instead of a silent empty list.
func WithScheduleManager(m ScheduleManager) ServerOption {
	return func(s *Server) {
		s.schedules = m
	}
}

func (s *Server) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	if s.schedules == nil {
		writeError(w, http.StatusServiceUnavailable, "scheduler not available in this process")
		return
	}
	name := r.PathValue("name")
	if p := s.dagPath(name); p == "" {
		writeError(w, http.StatusNotFound, "DAG not found: "+name)
		return
	}
	views := s.schedules.ListSchedules(name)
	out := make([]Schedule, 0, len(views))
	for _, v := range views {
		out = append(out, viewToAPI(v))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	if s.schedules == nil {
		writeError(w, http.StatusServiceUnavailable, "scheduler not available in this process")
		return
	}
	name := r.PathValue("name")
	if p := s.dagPath(name); p == "" {
		writeError(w, http.StatusNotFound, "DAG not found: "+name)
		return
	}

	var req CreateScheduleRequest
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Cron == "" {
		writeError(w, http.StatusBadRequest, "cron is required")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	view, err := s.schedules.AddRuntimeSchedule(name, req.Cron, req.Params, enabled)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, viewToAPI(view))
}

func (s *Server) handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	if s.schedules == nil {
		writeError(w, http.StatusServiceUnavailable, "scheduler not available in this process")
		return
	}
	name := r.PathValue("name")
	scheduleID := r.PathValue("schedule_id")

	err := s.schedules.RemoveRuntimeSchedule(name, scheduleID)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, scheduler.ErrYAMLSchedule):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusNotFound, err.Error())
	}
}

func (s *Server) handlePatchSchedule(w http.ResponseWriter, r *http.Request) {
	if s.schedules == nil {
		writeError(w, http.StatusServiceUnavailable, "scheduler not available in this process")
		return
	}
	name := r.PathValue("name")
	scheduleID := r.PathValue("schedule_id")

	var req UpdateScheduleRequest
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled is required")
		return
	}

	view, err := s.schedules.SetRuntimeScheduleEnabled(name, scheduleID, *req.Enabled)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, viewToAPI(view))
	case errors.Is(err, scheduler.ErrYAMLSchedule):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusNotFound, err.Error())
	}
}

func viewToAPI(v scheduler.ScheduleView) Schedule {
	out := Schedule{
		ID:      v.ID,
		Cron:    v.Cron,
		Source:  v.Source,
		Enabled: v.Enabled,
		Params:  v.Params,
	}
	if !v.NextRun.IsZero() {
		out.NextRun = v.NextRun.UTC().Format(time.RFC3339)
	}
	return out
}
