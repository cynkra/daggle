package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/cynkra/daggle/cache"
	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/engine"
	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var planCmd = &cobra.Command{
	Use:   "plan <dag-name>",
	Short: "Show execution plan with cache status",
	Args:  cobra.ExactArgs(1),
	RunE:  planDAG,
}

func init() {
	rootCmd.AddCommand(planCmd)
}

// PlanEntry describes the plan status for a single step.
type PlanEntry struct {
	StepID string
	Status string // "cached", "outdated", "no-cache"
	Reason string
}

func planDAG(_ *cobra.Command, args []string) error {
	dagName := args[0]
	applyOverrides()

	// Resolve and parse DAG file
	dagPath := resolveDAGPath(dagName)
	d, err := dag.ParseFile(dagPath)
	if err != nil {
		return fmt.Errorf("parse DAG %q: %w", dagName, err)
	}

	// Apply base.yaml defaults
	baseDefaults, err := dag.LoadBaseDefaults(filepath.Dir(dagPath))
	if err != nil {
		return fmt.Errorf("load base.yaml: %w", err)
	}
	dag.ApplyBaseDefaults(d, baseDefaults)

	// Expand templates (no params for plan)
	expanded, err := dag.ExpandDAG(d, nil)
	if err != nil {
		return fmt.Errorf("expand templates: %w", err)
	}

	// Expand matrix steps
	expanded.Steps = dag.ExpandMatrix(expanded.Steps)

	// Topo sort
	tiers, err := dag.TopoSort(expanded.Steps)
	if err != nil {
		return fmt.Errorf("topo sort: %w", err)
	}

	// Flatten tiers into ordered steps
	var steps []dag.Step
	for _, tier := range tiers {
		steps = append(steps, tier...)
	}

	// Set up cache store
	cacheDir := filepath.Join(state.DataDir(), "cache")
	store := cache.NewStore(cacheDir)

	// Compute plan entries
	entries := computePlan(expanded, steps, store)

	// Count stats
	var cached, outdated int
	for _, e := range entries {
		switch e.Status {
		case "cached":
			cached++
		case "outdated":
			outdated++
		}
	}

	// Print summary
	fmt.Printf("%s: %d steps, %d cached, %d outdated\n\n", expanded.Name, len(steps), cached, outdated)

	// Print table
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "STEP\tSTATUS\tREASON\n")
	for _, e := range entries {
		reason := e.Reason
		if reason == "" {
			reason = "-"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", e.StepID, e.Status, reason)
	}
	_ = w.Flush()

	return nil
}

// computePlan builds plan entries for all steps, checking cache status.
func computePlan(d *dag.DAG, steps []dag.Step, store *cache.Store) []PlanEntry {
	entries := make([]PlanEntry, 0, len(steps))

	// Track which steps are outdated so we can flag downstream steps
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

		// Check if any upstream dependency is outdated
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

		// Compute cache key and check
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
			entries = append(entries, PlanEntry{
				StepID: step.ID,
				Status: "outdated",
				Reason: reasonForOutdated(step, d),
			})
		}
	}

	return entries
}

// reasonForOutdated returns a human-readable reason why a step is outdated.
func reasonForOutdated(step dag.Step, _ *dag.DAG) string {
	switch {
	case step.Script != "":
		return fmt.Sprintf("script %s changed", step.Script)
	case step.RExpr != "":
		return "r_expr changed"
	case step.Command != "":
		return "command changed"
	default:
		return "inputs changed"
	}
}
