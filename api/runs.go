package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cynkra/daggle/state"
)

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	runs, err := state.ListRuns(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list runs: "+err.Error())
		return
	}

	var result []RunSummary
	for _, run := range runs {
		rs := RunSummary{
			RunID:   run.ID,
			Started: formatTime(run.StartTime),
			Status:  state.RunStatus(run.Dir),
		}

		if meta, err := state.ReadMeta(run.Dir); err == nil {
			if !meta.EndTime.IsZero() {
				rs.DurationSeconds = meta.EndTime.Sub(meta.StartTime).Seconds()
			}
			if meta.DAGHash != "" && len(meta.DAGHash) >= 12 {
				rs.DAGHash = meta.DAGHash[:12]
			}
		}

		result = append(result, rs)
	}

	if result == nil {
		result = []RunSummary{}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	runID := r.PathValue("run_id")

	run, err := s.findRun(name, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	detail := s.buildRunDetail(name, run)
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	runID := r.PathValue("run_id")

	run, err := s.findRun(name, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	status := state.RunStatus(run.Dir)
	if status != "running" && status != "waiting" {
		writeError(w, http.StatusConflict, "run is not running (status: "+status+")")
		return
	}

	cancelFile := filepath.Join(run.Dir, "cancel.requested")
	if err := os.WriteFile(cancelFile, []byte("cancel"), 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "write cancel request: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "cancel_requested",
		"run_id":  run.ID,
		"message": "the run will stop after the current tier completes",
	})
}

func (s *Server) handleCleanup(w http.ResponseWriter, r *http.Request) {
	var req CleanupRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.OlderThan == "" {
		writeError(w, http.StatusBadRequest, "older_than is required")
		return
	}

	threshold, err := state.ParseDurationWithDays(req.OlderThan)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid duration: "+err.Error())
		return
	}

	result, err := state.CleanupRuns(threshold)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cleanup failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, CleanupResponse{
		Removed:    result.Removed,
		FreedBytes: result.FreedBytes,
		Freed:      formatBytes(result.FreedBytes),
	})
}

func (s *Server) findRun(dagName, runID string) (*state.RunInfo, error) {
	if runID == "latest" {
		run, err := state.LatestRun(dagName)
		if err != nil {
			return nil, err
		}
		if run == nil {
			return nil, &notFoundError{"no runs found for DAG " + dagName}
		}
		return run, nil
	}

	runs, err := state.ListRuns(dagName)
	if err != nil {
		return nil, err
	}
	for _, r := range runs {
		if r.ID == runID {
			return &r, nil
		}
	}
	return nil, &notFoundError{"run " + runID + " not found for DAG " + dagName}
}

func (s *Server) buildRunDetail(dagName string, run *state.RunInfo) RunDetail {
	detail := RunDetail{
		RunID:   run.ID,
		DAGName: dagName,
		Status:  state.RunStatus(run.Dir),
		Started: formatTime(run.StartTime),
	}

	if meta, err := state.ReadMeta(run.Dir); err == nil {
		detail.Ended = formatTime(meta.EndTime)
		if !meta.EndTime.IsZero() {
			detail.DurationSeconds = meta.EndTime.Sub(meta.StartTime).Seconds()
		}
		if meta.DAGHash != "" && len(meta.DAGHash) >= 12 {
			detail.DAGHash = meta.DAGHash[:12]
		}
		detail.RVersion = meta.RVersion
		detail.Platform = meta.Platform
		detail.Params = meta.Params
	}

	detail.Steps = s.buildStepSummaries(run.Dir)
	return detail
}

type notFoundError struct{ msg string }

func (e *notFoundError) Error() string { return e.msg }

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
