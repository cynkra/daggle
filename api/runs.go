package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

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

	threshold, err := parseDurationWithDays(req.OlderThan)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid duration: "+err.Error())
		return
	}

	cutoff := time.Now().Add(-threshold)
	runsDir := state.RunsDir()

	var removed int
	var freedBytes int64

	dagDirs, err := os.ReadDir(runsDir)
	if err != nil {
		writeJSON(w, http.StatusOK, CleanupResponse{Removed: 0, FreedBytes: 0, Freed: "0 B"})
		return
	}

	for _, dagDir := range dagDirs {
		if !dagDir.IsDir() {
			continue
		}
		dagPath := filepath.Join(runsDir, dagDir.Name())
		dateDirs, _ := os.ReadDir(dagPath)
		for _, dateDir := range dateDirs {
			if !dateDir.IsDir() {
				continue
			}
			datePath := filepath.Join(dagPath, dateDir.Name())
			runDirs, _ := os.ReadDir(datePath)
			for _, runDir := range runDirs {
				if !runDir.IsDir() || !strings.HasPrefix(runDir.Name(), "run_") {
					continue
				}
				runPath := filepath.Join(datePath, runDir.Name())
				info, err := runDir.Info()
				if err != nil {
					continue
				}
				if info.ModTime().Before(cutoff) {
					size := dirSize(runPath)
					if err := os.RemoveAll(runPath); err != nil {
						continue
					}
					removed++
					freedBytes += size
				}
			}
			if remaining, _ := os.ReadDir(datePath); len(remaining) == 0 {
				_ = os.Remove(datePath)
			}
		}
		if remaining, _ := os.ReadDir(dagPath); len(remaining) == 0 {
			_ = os.Remove(dagPath)
		}
	}

	writeJSON(w, http.StatusOK, CleanupResponse{
		Removed:    removed,
		FreedBytes: freedBytes,
		Freed:      formatBytes(freedBytes),
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

func dirSize(path string) int64 {
	var size int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		size += info.Size()
		return nil
	})
	return size
}

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
