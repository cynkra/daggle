package api

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/engine"
	"github.com/cynkra/daggle/executor"
	"github.com/cynkra/daggle/state"
)

func (s *Server) handleListDAGs(w http.ResponseWriter, _ *http.Request) {
	entries, err := os.ReadDir(s.dagDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read DAG directory: "+err.Error())
		return
	}

	var dags []DAGSummary
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		if name == "base.yaml" || name == "base.yml" {
			continue
		}

		dagName := strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
		path := filepath.Join(s.dagDir, name)

		d, err := dag.ParseFile(path)
		if err != nil {
			continue // skip invalid DAGs
		}

		summary := DAGSummary{
			Name:  dagName,
			Steps: len(d.Steps),
		}
		if d.Trigger != nil {
			summary.Schedule = d.Trigger.Schedule
		}

		if run, err := state.LatestRun(dagName); err == nil && run != nil {
			summary.LastStatus = state.RunStatus(run.Dir)
			summary.LastRun = formatTime(run.StartTime)
		}

		dags = append(dags, summary)
	}

	if dags == nil {
		dags = []DAGSummary{} // return [] not null
	}
	writeJSON(w, http.StatusOK, dags)
}

func (s *Server) handleGetDAG(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path := s.dagPath(name)
	if path == "" {
		writeError(w, http.StatusNotFound, "DAG "+name+" not found")
		return
	}

	d, err := dag.ParseFile(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "parse DAG: "+err.Error())
		return
	}

	var stepIDs []string
	for _, step := range d.Steps {
		stepIDs = append(stepIDs, step.ID)
	}

	detail := DAGDetail{
		Name:    name,
		Steps:   len(d.Steps),
		StepIDs: stepIDs,
		Workdir: d.Workdir,
	}
	if d.RVersion != "" {
		detail.RVersion = d.RVersion
	}
	if d.Trigger != nil {
		detail.Schedule = d.Trigger.Schedule
	}

	if run, err := state.LatestRun(name); err == nil && run != nil {
		detail.LastStatus = state.RunStatus(run.Dir)
		detail.LastRunID = run.ID
		detail.LastRun = formatTime(run.StartTime)
	}

	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleTriggerRun(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path := s.dagPath(name)
	if path == "" {
		writeError(w, http.StatusNotFound, "DAG "+name+" not found")
		return
	}

	var req TriggerRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	d, err := dag.ParseFile(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "parse DAG: "+err.Error())
		return
	}

	// Apply base defaults
	baseDefaults, _ := dag.LoadBaseDefaults(filepath.Dir(path))
	dag.ApplyBaseDefaults(d, baseDefaults)

	// Expand templates
	expanded, err := dag.ExpandDAG(d, req.Params)
	if err != nil {
		writeError(w, http.StatusBadRequest, "expand templates: "+err.Error())
		return
	}

	// Resolve secrets
	if err := dag.ResolveEnv(expanded.Env); err != nil {
		writeError(w, http.StatusInternalServerError, "resolve env: "+err.Error())
		return
	}

	// Create run
	run, err := state.CreateRun(expanded.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create run: "+err.Error())
		return
	}

	// Build metadata
	dagHash, _ := dag.HashFile(path)
	meta := &state.RunMeta{
		RunID:         run.ID,
		DAGName:       expanded.Name,
		DAGHash:       dagHash,
		DAGPath:       path,
		Params:        req.Params,
		DaggleVersion: s.version,
		TriggerSource: "api",
	}

	// Execute async
	go func() {
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()

		eng := engine.New(expanded, run, executor.New)
		eng.SetMeta(meta)

		redactor := dag.NewRedactor(expanded.Env)
		eng.SetRedactor(redactor)

		_ = eng.Run(ctx)
	}()

	writeJSON(w, http.StatusCreated, TriggerResponse{
		RunID:  run.ID,
		Status: "started",
	})
}

