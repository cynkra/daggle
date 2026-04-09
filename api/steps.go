package api

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cynkra/daggle/executor"
	"github.com/cynkra/daggle/state"
)

func (s *Server) handleListSteps(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	runID := r.PathValue("run_id")

	run, err := s.findRun(name, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	steps := s.buildStepSummaries(run.Dir)
	writeJSON(w, http.StatusOK, steps)
}

func (s *Server) handleStepLog(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	runID := r.PathValue("run_id")
	stepID := r.PathValue("step_id")

	run, err := s.findRun(name, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	stdout, _ := os.ReadFile(filepath.Join(run.Dir, stepID+".stdout.log"))
	stderr, _ := os.ReadFile(filepath.Join(run.Dir, stepID+".stderr.log"))

	writeJSON(w, http.StatusOK, StepLog{
		StepID: stepID,
		Stdout: string(stdout),
		Stderr: string(stderr),
	})
}

func (s *Server) handleApproveStep(w http.ResponseWriter, r *http.Request) {
	s.handleApproval(w, r, true)
}

func (s *Server) handleRejectStep(w http.ResponseWriter, r *http.Request) {
	s.handleApproval(w, r, false)
}

func (s *Server) handleApproval(w http.ResponseWriter, r *http.Request, approved bool) {
	name := r.PathValue("name")
	runID := r.PathValue("run_id")
	stepID := r.PathValue("step_id")

	run, err := s.findRun(name, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// Verify the step is waiting for approval
	events, err := state.ReadEvents(run.Dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read events: "+err.Error())
		return
	}

	waiting := false
	for _, e := range events {
		if e.StepID == stepID && e.Type == state.EventStepWaitApproval {
			waiting = true
		}
		if e.StepID == stepID && (e.Type == state.EventStepApproved || e.Type == state.EventStepRejected) {
			waiting = false
		}
	}

	if !waiting {
		writeError(w, http.StatusConflict, "step "+stepID+" is not waiting for approval")
		return
	}

	if err := executor.WriteApprovalEvent(run.Dir, stepID, approved); err != nil {
		writeError(w, http.StatusInternalServerError, "write approval: "+err.Error())
		return
	}

	action := "approved"
	if !approved {
		action = "rejected"
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"step_id": stepID,
		"status":  action,
	})
}

func (s *Server) buildStepSummaries(runDir string) []StepSummary {
	events, err := state.ReadEvents(runDir)
	if err != nil {
		return []StepSummary{}
	}

	type stepState struct {
		status   string
		duration time.Duration
		attempts int
		err      string
		message  string
		cached   bool
		cacheKey string
	}

	steps := make(map[string]*stepState)
	var order []string

	for _, e := range events {
		if e.StepID == "" {
			continue
		}
		ss, ok := steps[e.StepID]
		if !ok {
			ss = &stepState{}
			steps[e.StepID] = ss
			order = append(order, e.StepID)
		}
		switch e.Type {
		case state.EventStepStarted:
			ss.status = "running"
			ss.attempts = e.Attempt
		case state.EventStepCompleted:
			ss.status = "completed"
			if d, err := time.ParseDuration(e.Duration); err == nil {
				ss.duration = d
			}
			ss.attempts = e.Attempt
		case state.EventStepFailed:
			ss.status = "failed"
			if d, err := time.ParseDuration(e.Duration); err == nil {
				ss.duration = d
			}
			ss.attempts = e.Attempt
			ss.err = e.Error
		case state.EventStepRetrying:
			ss.status = "retrying"
		case state.EventStepWaitApproval:
			ss.status = "waiting"
			ss.message = e.Message
		case state.EventStepApproved:
			ss.status = "approved"
		case state.EventStepRejected:
			ss.status = "rejected"
		case "step_skipped":
			ss.status = "skipped"
		case state.EventStepCached:
			ss.status = "cached"
			ss.cached = true
			ss.cacheKey = e.CacheKey
		}
	}

	result := make([]StepSummary, 0, len(order))
	for _, id := range order {
		ss := steps[id]
		result = append(result, StepSummary{
			StepID:          id,
			Status:          ss.status,
			DurationSeconds: ss.duration.Seconds(),
			Attempts:        ss.attempts,
			Error:           ss.err,
			Message:         ss.message,
			Cached:          ss.cached,
			CacheKey:        ss.cacheKey,
		})
	}

	return result
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	projects, _ := state.LoadProjects()
	sources := s.sources()

	// Count total DAGs across all sources
	totalDAGs := 0
	for _, src := range sources {
		totalDAGs += countDAGs(src.Dir)
	}

	resp := HealthResponse{
		Status:        "ok",
		Version:       s.version,
		UptimeSeconds: time.Since(s.started).Seconds(),
		Projects:      len(projects),
		DAGs:          totalDAGs,
	}

	// Scheduler status (if available)
	if s.schedulerStatus != nil {
		resp.Scheduler = s.schedulerStatus()
	}

	// Find most recent run across all DAGs
	dagNames := state.CollectDAGNames(sources)
	var latestRun *state.RunInfo
	for dagName := range dagNames {
		run, err := state.LatestRun(dagName)
		if err != nil || run == nil {
			continue
		}
		if latestRun == nil || run.StartTime.After(latestRun.StartTime) {
			latestRun = run
		}
	}
	if latestRun != nil {
		resp.LastRun = &LastRunInfo{
			DAGName: latestRun.DAGName,
			RunID:   latestRun.ID,
			Status:  state.RunStatus(latestRun.Dir),
			Started: formatTime(latestRun.StartTime),
		}
	}

	writeJSON(w, http.StatusOK, resp)
}
