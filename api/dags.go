package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/cynkra/daggle/cache"
	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/engine"
	"github.com/cynkra/daggle/executor"
	"github.com/cynkra/daggle/state"
)

func (s *Server) handleListDAGs(w http.ResponseWriter, _ *http.Request) {
	var dags []DAGSummary

	for _, src := range s.sources() {
		entries, err := os.ReadDir(src.Dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			continue
		}

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
			path := filepath.Join(src.Dir, name)

			d, err := dag.ParseFile(path)
			if err != nil {
				continue
			}

			summary := DAGSummary{
				Name:    dagName,
				Steps:   len(d.Steps),
				Project: src.Name,
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
	}

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

	expanded, err := dag.LoadAndExpand(path, req.Params)
	if err != nil {
		writeError(w, http.StatusBadRequest, "load DAG: "+err.Error())
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

