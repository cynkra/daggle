package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cynkra/daggle/cache"
	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/internal/executor"
	"github.com/cynkra/daggle/state"
)

// ExecutorFactory creates an executor for a given step.
type ExecutorFactory func(dag.Step) executor.Executor

// Config bundles everything needed to construct an Engine. The first three
// fields are required; the rest are optional but production callers should
// set them — in particular Redactor, or secret values leak into events.
type Config struct {
	DAG         *dag.DAG
	Run         *state.RunInfo
	ExecFactory ExecutorFactory

	Meta          *state.RunMeta
	Redactor      *dag.Redactor
	CacheStore    *cache.Store
	RenvLibPath   string
	Notifications map[string]state.NotificationChannel
}

// Engine orchestrates the execution of a DAG.
type Engine struct {
	dag     *dag.DAG
	execFn  ExecutorFactory
	events  *state.EventWriter
	runInfo *state.RunInfo
	meta    *state.RunMeta
	logger  *slog.Logger

	// renvLibPath, if non-empty, is injected as R_LIBS_USER for all steps.
	renvLibPath string

	// cacheStore, if non-nil, enables step-level caching.
	cacheStore *cache.Store

	// redactor replaces secret values in event error messages.
	redactor *dag.Redactor

	// hookEnvExtras holds additional env vars injected into hooks at run end.
	hookEnvExtras []string

	// notifications are named channels available for notify: hooks.
	notifications map[string]state.NotificationChannel

	// outputs collects ::daggle-output:: values from completed steps.
	// Keys are namespaced: DAGGLE_OUTPUT_<STEP_ID>_<KEY>
	mu      sync.Mutex
	outputs map[string]string

	// sem bounds concurrent step execution when DAG.MaxParallel > 0.
	// nil means unbounded.
	sem chan struct{}
}

// New creates an Engine from the given Config. Returns an error if any of
// the three required fields (DAG, Run, ExecFactory) is nil.
func New(cfg Config) (*Engine, error) {
	if cfg.DAG == nil {
		return nil, errors.New("engine: Config.DAG is required")
	}
	if cfg.Run == nil {
		return nil, errors.New("engine: Config.Run is required")
	}
	if cfg.ExecFactory == nil {
		return nil, errors.New("engine: Config.ExecFactory is required")
	}
	e := &Engine{
		dag:           cfg.DAG,
		execFn:        cfg.ExecFactory,
		events:        state.NewEventWriter(cfg.Run.Dir),
		runInfo:       cfg.Run,
		meta:          cfg.Meta,
		redactor:      cfg.Redactor,
		cacheStore:    cfg.CacheStore,
		renvLibPath:   cfg.RenvLibPath,
		notifications: cfg.Notifications,
		logger:        slog.Default(),
		outputs:       make(map[string]string),
	}
	if cfg.DAG.MaxParallel > 0 {
		e.sem = make(chan struct{}, cfg.DAG.MaxParallel)
	}
	return e, nil
}

// Run executes the DAG by walking tiers in topological order.
// Steps within a tier run in parallel. If any step fails (after retries),
// remaining tiers are skipped and the run is marked as failed.
func (e *Engine) Run(ctx context.Context) error {
	defer func() { _ = e.events.Close() }()
	// Reject notify: references to channels missing from the loaded config
	// before we do anything else. Falls through for DAGs without notify hooks.
	channels := make(map[string]bool, len(e.notifications))
	for name := range e.notifications {
		channels[name] = true
	}
	if err := dag.ValidateChannels(e.dag, channels); err != nil {
		return err
	}

	// Expand matrix steps before topo sort
	e.dag.Steps = dag.ExpandMatrix(e.dag.Steps)

	tiers, err := dag.TopoSort(e.dag.Steps)
	if err != nil {
		return fmt.Errorf("topo sort: %w", err)
	}

	e.writeEvent(state.Event{Type: state.EventRunStarted})
	e.logger.Info("run started", "dag", e.dag.Name, "run_id", e.runInfo.ID)

	// Write initial metadata
	if e.meta != nil {
		e.writeMeta(e.runInfo.Dir, e.meta)
	}

	// Build environment: DAG-level env + daggle metadata + renv
	env := buildEnv(e.dag, e.runInfo, e.renvLibPath)

	var runErr error
	for tierIdx, tier := range tiers {
		// Check for cancel request between tiers
		if e.isCancelled() {
			e.writeEvent(state.Event{
				Type:  state.EventRunFailed,
				Error: "run cancelled",
			})
			e.logger.Info("run cancelled", "dag", e.dag.Name)
			runErr = fmt.Errorf("run cancelled")
			break
		}

		e.logger.Info("executing tier", "tier", tierIdx, "steps", stepIDs(tier))

		if err := e.runTier(ctx, tier, env); err != nil {
			e.writeEvent(state.Event{
				Type:  state.EventRunFailed,
				Error: err.Error(),
			})
			e.logger.Error("run failed", "dag", e.dag.Name, "error", err)
			runErr = err
			break
		}
	}

	if runErr == nil {
		e.writeEvent(state.Event{Type: state.EventRunCompleted})
		e.logger.Info("run completed", "dag", e.dag.Name, "run_id", e.runInfo.ID)
	}

	// Update metadata with final status
	if e.meta != nil {
		e.meta.EndTime = time.Now()
		if runErr == nil {
			e.meta.Status = "completed"
		} else {
			e.meta.Status = "failed"
		}
		e.writeMeta(e.runInfo.Dir, e.meta)
	}

	// Build richer hook context
	e.hookEnvExtras = e.buildHookExtras(runErr)

	// Run lifecycle hooks
	e.runHooks(ctx, runErr)

	return runErr
}

func (e *Engine) runTier(ctx context.Context, steps []dag.Step, env []string) error {
	var mu sync.Mutex
	var errs []error
	var wg sync.WaitGroup

	for _, step := range steps {
		wg.Add(1)
		go func(s dag.Step) {
			defer wg.Done()
			if e.sem != nil {
				e.sem <- struct{}{}
				defer func() { <-e.sem }()
			}
			if err := e.runStep(ctx, s, env); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("step %q failed: %w", s.ID, err))
				mu.Unlock()
			}
		}(step)
	}

	wg.Wait()
	return errors.Join(errs...)
}

func (e *Engine) runStep(ctx context.Context, step dag.Step, env []string) error {
	if skip, err := e.checkWhenCondition(ctx, step, env); err != nil {
		return err
	} else if skip {
		return nil
	}

	if err := e.checkPreconditions(ctx, step, env); err != nil {
		return err
	}

	workdir := e.dag.ResolveWorkdir(step)

	if err := e.checkFreshness(step, workdir); err != nil {
		return err
	}

	if hit, err := e.tryCacheHit(step, workdir, env); err != nil {
		return err
	} else if hit {
		return nil
	}

	stepEnv := e.buildStepEnv(env, step)
	return e.executeWithRetries(ctx, step, workdir, stepEnv)
}

// buildStepEnv merges base env with step-level env vars and cached outputs
// from prior steps.
func (e *Engine) buildStepEnv(baseEnv []string, step dag.Step) []string {
	env := make([]string, len(baseEnv))
	copy(env, baseEnv)
	for k, v := range step.Env {
		env = append(env, k+"="+v.Value)
	}
	e.mu.Lock()
	for k, v := range e.outputs {
		env = append(env, k+"="+v)
	}
	e.mu.Unlock()
	return env
}

// isCancelled checks if a cancel.requested file exists in the run directory.
func (e *Engine) isCancelled() bool {
	_, err := os.Stat(filepath.Join(e.runInfo.Dir, "cancel.requested"))
	return err == nil
}

func sanitize(s string) string {
	r := strings.NewReplacer(" ", "_", "/", "_", ":", "_")
	return r.Replace(s)
}

func (e *Engine) writeEvent(ev state.Event) {
	if e.redactor != nil {
		ev.Error = e.redactor.Redact(ev.Error)
		ev.ErrorDetail = e.redactor.Redact(ev.ErrorDetail)
	}
	if err := e.events.Write(ev); err != nil {
		e.logger.Warn("failed to write event", "type", ev.Type, "error", err)
	}
}

func (e *Engine) writeMeta(dir string, meta *state.RunMeta) {
	if err := state.WriteMeta(dir, meta); err != nil {
		e.logger.Warn("failed to write metadata", "error", err)
	}
}

func buildEnv(d *dag.DAG, runInfo *state.RunInfo, renvLibPath string) []string {
	var env []string
	for k, v := range d.Env {
		env = append(env, k+"="+v.Value)
	}
	env = append(env,
		"DAGGLE_RUN_ID="+runInfo.ID,
		"DAGGLE_DAG_NAME="+d.Name,
		"DAGGLE_RUN_DIR="+runInfo.Dir,
	)
	// Inject R_LIBS_USER for renv, unless user explicitly set it in DAG env
	if renvLibPath != "" {
		if _, userSet := d.Env["R_LIBS_USER"]; !userSet {
			env = append(env, "R_LIBS_USER="+renvLibPath)
		}
	}
	return env
}

func stepIDs(steps []dag.Step) []string {
	ids := make([]string, len(steps))
	for i, s := range steps {
		ids[i] = s.ID
	}
	return ids
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
