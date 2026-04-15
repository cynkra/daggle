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

	run, err := state.FindRun(name, runID)
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

	run, err := state.FindRun(name, runID)
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
	if err := os.WriteFile(cancelFile, []byte("cancel"), 0o644); err != nil {
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

func (s *Server) handleCompareRuns(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	run1ID := r.URL.Query().Get("run1")
	run2ID := r.URL.Query().Get("run2")

	if run1ID == "" || run2ID == "" {
		writeError(w, http.StatusBadRequest, "both run1 and run2 query parameters are required")
		return
	}

	run1, err := state.FindRun(name, run1ID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	run2, err := state.FindRun(name, run2ID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	resp := s.buildCompareResponse(run1, run2)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) buildCompareResponse(run1, run2 *state.RunInfo) CompareResponse {
	var resp CompareResponse

	// Collect outputs for both runs.
	out1 := s.collectRunOutputs(run1.Dir)
	out2 := s.collectRunOutputs(run2.Dir)

	// Build combined key set and find differences.
	type oKey struct{ step, key string }
	allKeys := make(map[oKey]bool)
	for k := range out1 {
		allKeys[k] = true
	}
	for k := range out2 {
		allKeys[k] = true
	}

	for k := range allKeys {
		v1, v2 := out1[k], out2[k]
		if v1 != v2 {
			resp.OutputsDiff = append(resp.OutputsDiff, OutputDiff{
				StepID: k.step,
				Key:    k.key,
				Value1: v1,
				Value2: v2,
			})
		}
	}
	if resp.OutputsDiff == nil {
		resp.OutputsDiff = []OutputDiff{}
	}

	// Duration comparison.
	meta1, _ := state.ReadMeta(run1.Dir)
	meta2, _ := state.ReadMeta(run2.Dir)

	if meta1 != nil && meta2 != nil {
		d1 := meta1.EndTime.Sub(meta1.StartTime).Seconds()
		d2 := meta2.EndTime.Sub(meta2.StartTime).Seconds()
		if meta1.EndTime.IsZero() {
			d1 = 0
		}
		if meta2.EndTime.IsZero() {
			d2 = 0
		}
		resp.DurationDiff = DurationDiff{
			Run1Seconds: d1,
			Run2Seconds: d2,
			DiffSeconds: d2 - d1,
		}
	}

	// DAG hash comparison.
	hash1, hash2 := "", ""
	if meta1 != nil {
		hash1 = meta1.DAGHash
	}
	if meta2 != nil {
		hash2 = meta2.DAGHash
	}
	resp.MetaDiff = MetaDiff{
		DAGHash1: hash1,
		DAGHash2: hash2,
		Changed:  hash1 != hash2,
	}

	return resp
}

func (s *Server) collectRunOutputs(runDir string) map[struct{ step, key string }]string {
	events, err := state.ReadEvents(runDir)
	if err != nil {
		return nil
	}

	stepIDs := make(map[string]bool)
	for _, e := range events {
		if e.StepID != "" {
			stepIDs[e.StepID] = true
		}
	}

	type oKey = struct{ step, key string }
	result := make(map[oKey]string)
	for stepID := range stepIDs {
		markers := ParseOutputMarkers(runDir, stepID)
		for k, v := range markers {
			result[oKey{step: stepID, key: k}] = v
		}
	}
	return result
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

const byteUnits = "KMGTPE"

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
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), byteUnits[exp])
}
