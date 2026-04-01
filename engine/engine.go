package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/schochastics/rdag/dag"
	"github.com/schochastics/rdag/executor"
	"github.com/schochastics/rdag/state"
)

// ExecutorFactory creates an executor for a given step.
type ExecutorFactory func(dag.Step) executor.Executor

// Engine orchestrates the execution of a DAG.
type Engine struct {
	dag     *dag.DAG
	execFn  ExecutorFactory
	events  *state.EventWriter
	runInfo *state.RunInfo
	logger  *slog.Logger
}

// New creates a new Engine.
func New(d *dag.DAG, runInfo *state.RunInfo, execFn ExecutorFactory) *Engine {
	return &Engine{
		dag:     d,
		execFn:  execFn,
		events:  state.NewEventWriter(runInfo.Dir),
		runInfo: runInfo,
		logger:  slog.Default(),
	}
}

// Run executes the DAG by walking tiers in topological order.
// Steps within a tier run in parallel. If any step fails (after retries),
// remaining tiers are skipped and the run is marked as failed.
func (e *Engine) Run(ctx context.Context) error {
	tiers, err := dag.TopoSort(e.dag.Steps)
	if err != nil {
		return fmt.Errorf("topo sort: %w", err)
	}

	e.events.Write(state.Event{Type: state.EventRunStarted})
	e.logger.Info("run started", "dag", e.dag.Name, "run_id", e.runInfo.ID)

	// Build environment: DAG-level env + rdag metadata
	env := buildEnv(e.dag, e.runInfo)

	for tierIdx, tier := range tiers {
		e.logger.Info("executing tier", "tier", tierIdx, "steps", stepIDs(tier))

		if err := e.runTier(ctx, tier, env); err != nil {
			e.events.Write(state.Event{
				Type:  state.EventRunFailed,
				Error: err.Error(),
			})
			e.logger.Error("run failed", "dag", e.dag.Name, "error", err)
			return err
		}
	}

	e.events.Write(state.Event{Type: state.EventRunCompleted})
	e.logger.Info("run completed", "dag", e.dag.Name, "run_id", e.runInfo.ID)
	return nil
}

func (e *Engine) runTier(ctx context.Context, steps []dag.Step, env []string) error {
	var mu sync.Mutex
	var firstErr error
	var wg sync.WaitGroup

	for _, step := range steps {
		wg.Add(1)
		go func(s dag.Step) {
			defer wg.Done()
			if err := e.runStep(ctx, s, env); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("step %q failed: %w", s.ID, err)
				}
				mu.Unlock()
			}
		}(step)
	}

	wg.Wait()
	return firstErr
}

func (e *Engine) runStep(ctx context.Context, step dag.Step, env []string) error {
	exec := e.execFn(step)
	if exec == nil {
		return fmt.Errorf("no executor for step %q", step.ID)
	}

	// Merge step-level env
	stepEnv := env
	for k, v := range step.Env {
		stepEnv = append(stepEnv, k+"="+v)
	}

	// Apply step-level timeout
	stepCtx := ctx
	if timeout, err := step.ParseTimeout(); err == nil && timeout > 0 {
		var cancel context.CancelFunc
		stepCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	maxAttempts := step.MaxAttempts()
	var lastResult executor.Result

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		e.events.Write(state.Event{
			Type:    state.EventStepStarted,
			StepID:  step.ID,
			Attempt: attempt,
		})
		e.logger.Info("step started", "step", step.ID, "attempt", attempt)

		lastResult = exec.Run(stepCtx, step, e.runInfo.Dir, stepEnv)

		if lastResult.Err == nil {
			e.events.Write(state.Event{
				Type:     state.EventStepCompleted,
				StepID:   step.ID,
				ExitCode: lastResult.ExitCode,
				Duration: lastResult.Duration.String(),
				Attempt:  attempt,
			})
			e.logger.Info("step completed", "step", step.ID, "duration", lastResult.Duration)
			return nil
		}

		if attempt < maxAttempts {
			e.events.Write(state.Event{
				Type:    state.EventStepRetrying,
				StepID:  step.ID,
				Error:   lastResult.Err.Error(),
				Attempt: attempt,
			})
			e.logger.Warn("step failed, retrying", "step", step.ID, "attempt", attempt, "error", lastResult.Err)
			time.Sleep(time.Duration(attempt) * time.Second) // simple linear backoff
		}
	}

	e.events.Write(state.Event{
		Type:     state.EventStepFailed,
		StepID:   step.ID,
		ExitCode: lastResult.ExitCode,
		Error:    lastResult.Err.Error(),
		Duration: lastResult.Duration.String(),
		Attempt:  maxAttempts,
	})
	e.logger.Error("step failed", "step", step.ID, "error", lastResult.Err)
	return lastResult.Err
}

func buildEnv(d *dag.DAG, runInfo *state.RunInfo) []string {
	var env []string
	for k, v := range d.Env {
		env = append(env, k+"="+v)
	}
	env = append(env,
		"RDAG_RUN_ID="+runInfo.ID,
		"RDAG_DAG_NAME="+d.Name,
		"RDAG_RUN_DIR="+runInfo.Dir,
	)
	return env
}

func stepIDs(steps []dag.Step) []string {
	ids := make([]string, len(steps))
	for i, s := range steps {
		ids[i] = s.ID
	}
	return ids
}
