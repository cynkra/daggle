package api

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/cynkra/daggle/cache"
	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/internal/engine"
	"github.com/cynkra/daggle/internal/executor"
	"github.com/cynkra/daggle/state"
)

func (s *Server) handleListDAGs(w http.ResponseWriter, r *http.Request) {
	filters := dag.Filters{
		Tag:   r.URL.Query().Get("tag"),
		Team:  r.URL.Query().Get("team"),
		Owner: r.URL.Query().Get("owner"),
	}

	var dags []DAGSummary

	_ = state.WalkDAGFiles(s.sources(), func(src state.DAGSource, path string) error {
		d, err := dag.ParseFileCached(path)
		if err != nil {
			return nil
		}

		if !filters.Match(d) {
			return nil
		}

		dagName := state.DAGNameFromFile(path)
		summary := DAGSummary{
			Name:        dagName,
			Steps:       len(d.Steps),
			Project:     src.Name,
			Owner:       d.Owner,
			Team:        d.Team,
			Description: d.Description,
			Tags:        d.Tags,
		}
		if d.Trigger != nil {
			summary.Schedule = d.Trigger.Schedule
		}

		if run, err := state.LatestRun(dagName); err == nil && run != nil {
			summary.LastStatus = state.RunStatus(run.Dir)
			summary.LastRun = formatTime(run.StartTime)
		}

		dags = append(dags, summary)
		return nil
	})

	if dags == nil {
		dags = []DAGSummary{}
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

	d, err := dag.ParseFileCached(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "parse DAG: "+err.Error())
		return
	}

	var stepIDs []string
	for _, step := range d.Steps {
		stepIDs = append(stepIDs, step.ID)
	}

	detail := DAGDetail{
		Name:        name,
		Steps:       len(d.Steps),
		StepIDs:     stepIDs,
		Workdir:     d.Workdir,
		Owner:       d.Owner,
		Team:        d.Team,
		Description: d.Description,
		Tags:        d.Tags,
		Exposures:   exposuresFromDAG(d),
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

// exposuresFromDAG copies dag.Exposure values into the API shape.
func exposuresFromDAG(d *dag.DAG) []ExposureEntry {
	if len(d.Exposures) == 0 {
		return nil
	}
	out := make([]ExposureEntry, len(d.Exposures))
	for i, e := range d.Exposures {
		out[i] = ExposureEntry{
			Name:        e.Name,
			Type:        e.Type,
			URL:         e.URL,
			Description: e.Description,
		}
	}
	return out
}

// handleGetImpact returns downstream DAGs and declared exposures for the DAG.
func (s *Server) handleGetImpact(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path := s.dagPath(name)
	if path == "" {
		writeError(w, http.StatusNotFound, "DAG "+name+" not found")
		return
	}

	target, err := dag.ParseFileCached(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "parse DAG: "+err.Error())
		return
	}

	resp := ImpactResponse{
		Name:       name,
		Downstream: []DownstreamDAGInfo{},
		Exposures:  exposuresFromDAG(target),
	}

	// Scan every DAG in every source for trigger.on_dag.name == target.
	_ = state.WalkDAGFiles(s.sources(), func(src state.DAGSource, path string) error {
		d, err := dag.ParseFileCached(path)
		if err != nil {
			return nil
		}
		if d.Trigger != nil && d.Trigger.OnDAG != nil && d.Trigger.OnDAG.Name == name {
			status := d.Trigger.OnDAG.Status
			if status == "" {
				status = "any"
			}
			resp.Downstream = append(resp.Downstream, DownstreamDAGInfo{
				Name:      d.Name,
				Project:   src.Name,
				TriggerOn: status,
			})
		}
		return nil
	})

	writeJSON(w, http.StatusOK, resp)
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

	expanded, err := dag.LoadAndExpand(path, req.Params)
	if err != nil {
		writeError(w, http.StatusBadRequest, "load DAG: "+err.Error())
		return
	}

	// Resolve secrets at DAG and step level. Step-level env vars also need
	// resolution so secret values feed into the Redactor below — without
	// this, secrets referenced only on a step would leak into events and
	// step logs.
	if err := dag.ResolveEnv(expanded.Env); err != nil {
		writeError(w, http.StatusInternalServerError, "resolve env: "+err.Error())
		return
	}
	for i := range expanded.Steps {
		if err := dag.ResolveEnv(expanded.Steps[i].Env); err != nil {
			writeError(w, http.StatusInternalServerError, "resolve step env: "+err.Error())
			return
		}
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
		ctx, cancel := context.WithCancel(s.ctx)
		defer cancel()

		engCfg := engine.Config{
			DAG:         expanded,
			Run:         run,
			ExecFactory: executor.New,
			Meta:        meta,
			Redactor:    dag.NewRedactor(dag.AllEnvMaps(expanded)...),
		}
		if cfg, err := state.LoadConfig(); err == nil && cfg.Notifications != nil {
			engCfg.Notifications = cfg.Notifications
		}
		eng, err := engine.New(engCfg)
		if err != nil {
			s.logger.Error("API-triggered run init failed", "dag", expanded.Name, "run_id", run.ID, "error", err)
			return
		}

		if err := eng.Run(ctx); err != nil {
			s.logger.Error("API-triggered run failed", "dag", expanded.Name, "run_id", run.ID, "error", err)
		}
	}()

	writeJSON(w, http.StatusCreated, TriggerResponse{
		RunID:  run.ID,
		Status: "started",
	})
}

func (s *Server) handleGetPlan(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path := s.dagPath(name)
	if path == "" {
		writeError(w, http.StatusNotFound, "DAG "+name+" not found")
		return
	}

	expanded, err := dag.LoadAndExpand(path, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, "load DAG: "+err.Error())
		return
	}

	// Expand matrix, topo sort
	expanded.Steps = dag.ExpandMatrix(expanded.Steps)
	tiers, err := dag.TopoSort(expanded.Steps)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "topo sort: "+err.Error())
		return
	}

	// Flatten tiers
	var steps []dag.Step
	for _, tier := range tiers {
		steps = append(steps, tier...)
	}

	// Set up cache store
	cacheDir := filepath.Join(state.DataDir(), "cache")
	store := cache.NewStore(cacheDir)

	// Build plan entries
	entries := buildPlanEntries(expanded, steps, store)

	writeJSON(w, http.StatusOK, entries)
}

// buildPlanEntries computes cache status for each step.
func buildPlanEntries(d *dag.DAG, steps []dag.Step, store *cache.Store) []PlanEntry {
	entries := make([]PlanEntry, 0, len(steps))
	outdatedSteps := make(map[string]bool)

	for _, step := range steps {
		if !step.Cache {
			entries = append(entries, PlanEntry{
				StepID: step.ID,
				Status: "no-cache",
				Reason: "caching not enabled",
			})
			continue
		}

		// Check upstream
		upstreamOutdated := ""
		for _, dep := range step.Depends {
			if outdatedSteps[dep] {
				upstreamOutdated = dep
				break
			}
		}

		if upstreamOutdated != "" {
			outdatedSteps[step.ID] = true
			entries = append(entries, PlanEntry{
				StepID: step.ID,
				Status: "outdated",
				Reason: fmt.Sprintf("upstream %s changed", upstreamOutdated),
			})
			continue
		}

		// Compute cache key
		stepType := engine.StepCacheType(step, d)
		var envVars []string
		for k, v := range d.Env {
			envVars = append(envVars, k+"="+v.Value)
		}
		for k, v := range step.Env {
			envVars = append(envVars, k+"="+v.Value)
		}
		cacheKey := cache.ComputeStepKey(stepType, envVars, nil, "")

		if _, ok := store.Lookup(d.Name, step.ID, cacheKey); ok {
			entries = append(entries, PlanEntry{
				StepID: step.ID,
				Status: "cached",
			})
		} else {
			outdatedSteps[step.ID] = true
			reason := "inputs changed"
			if step.Script != "" {
				entries = append(entries, PlanEntry{
					StepID: step.ID,
					Status: "outdated",
					Reason: fmt.Sprintf("script %s changed", step.Script),
				})
			} else {
				entries = append(entries, PlanEntry{
					StepID: step.ID,
					Status: "outdated",
					Reason: reason,
				})
			}
		}
	}

	return entries
}
