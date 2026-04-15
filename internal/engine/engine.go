package engine

import (
	"context"
	"crypto/sha256"
	"errors"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
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

	// outputs collects ::daggle-output:: values from completed steps.
	// Keys are namespaced: DAGGLE_OUTPUT_<STEP_ID>_<KEY>
	mu      sync.Mutex
	outputs map[string]string
}

// New creates a new Engine.
func New(d *dag.DAG, runInfo *state.RunInfo, execFn ExecutorFactory) *Engine {
	return &Engine{
		dag:     d,
		execFn:  execFn,
		events:  state.NewEventWriter(runInfo.Dir),
		runInfo: runInfo,
		logger:  slog.Default(),
		outputs: make(map[string]string),
	}
}

// SetMeta sets the run metadata to be written at start and updated at completion.
func (e *Engine) SetMeta(meta *state.RunMeta) {
	e.meta = meta
}

// SetRedactor sets the secret redactor for sanitizing event messages.
func (e *Engine) SetRedactor(r *dag.Redactor) {
	e.redactor = r
}

// SetCacheStore configures the step-level cache store.
func (e *Engine) SetCacheStore(s *cache.Store) {
	e.cacheStore = s
}

// SetRenvLibPath configures the renv library path to inject as R_LIBS_USER.
func (e *Engine) SetRenvLibPath(p string) {
	e.renvLibPath = p
}

// Run executes the DAG by walking tiers in topological order.
// Steps within a tier run in parallel. If any step fails (after retries),
// remaining tiers are skipped and the run is marked as failed.
func (e *Engine) Run(ctx context.Context) error {
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

// EventStepSkipped is emitted when a step is skipped due to a `when` condition.
const eventStepSkipped = "step_skipped"

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

// checkWhenCondition evaluates the step's when condition.
// Returns (true, nil) if the step should be skipped.
func (e *Engine) checkWhenCondition(ctx context.Context, step dag.Step, env []string) (bool, error) {
	if step.When == nil {
		return false, nil
	}
	skip, err := e.evaluateCondition(ctx, step.When, env)
	if err != nil {
		return false, fmt.Errorf("step %q when condition failed: %w", step.ID, err)
	}
	if skip {
		e.writeEvent(state.Event{Type: eventStepSkipped, StepID: step.ID})
		e.logger.Info("step skipped (condition not met)", "step", step.ID)
		return true, nil
	}
	return false, nil
}

// checkPreconditions evaluates all preconditions for a step, returning an error if any fail.
func (e *Engine) checkPreconditions(ctx context.Context, step dag.Step, env []string) error {
	for i, pre := range step.Preconditions {
		cond := &dag.StepCondition{RExpr: pre.RExpr, Command: pre.Command}
		if fail, err := e.evaluateCondition(ctx, cond, env); err != nil {
			return fmt.Errorf("step %q precondition %d failed: %w", step.ID, i+1, err)
		} else if fail {
			return fmt.Errorf("step %q precondition %d not met", step.ID, i+1)
		}
	}
	return nil
}

// checkFreshness verifies that source files referenced by a step are not stale.
func (e *Engine) checkFreshness(step dag.Step, workdir string) error {
	for _, fc := range step.Freshness {
		fcPath := fc.Path
		if !filepath.IsAbs(fcPath) {
			fcPath = filepath.Join(workdir, fcPath)
		}
		maxAge, err := time.ParseDuration(fc.MaxAge)
		if err != nil {
			return fmt.Errorf("step %q freshness path %q: invalid max_age %q: %w", step.ID, fc.Path, fc.MaxAge, err)
		}
		info, err := os.Stat(fcPath)
		if err != nil {
			return fmt.Errorf("step %q freshness path %q: %w", step.ID, fc.Path, err)
		}
		age := time.Since(info.ModTime())
		if age > maxAge {
			msg := fmt.Sprintf("step %q source %q is stale (age %s, max %s)", step.ID, fc.Path, age.Truncate(time.Second), maxAge)
			switch fc.OnStale {
			case "warn":
				e.logger.Warn(msg)
			default:
				return fmt.Errorf("%s", msg)
			}
		}
	}
	return nil
}

// tryCacheHit checks whether the step can be satisfied from cache.
// Returns (true, nil) if a cache hit was found and outputs were replayed.
func (e *Engine) tryCacheHit(step dag.Step, _ string, _ []string) (bool, error) {
	if !step.Cache || e.cacheStore == nil {
		return false, nil
	}
	cacheKey := e.computeCacheKey(step)
	entry, ok := e.cacheStore.Lookup(e.dag.Name, step.ID, cacheKey)
	if !ok {
		return false, nil
	}
	// Replay cached outputs
	e.collectOutputs(step.ID, entry.Outputs)
	e.writeEvent(state.Event{
		Type:   state.EventStepCached,
		StepID: step.ID,
		CacheInfo: &state.CacheInfo{
			CacheKey:    cacheKey,
			CachedRunID: entry.RunID,
		},
	})
	e.logger.Info("step cached", "step", step.ID, "cache_key", cacheKey[:12], "cached_run", entry.RunID)
	return true, nil
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

// executeWithRetries runs the step executor with retry logic, delegating
// post-execution handling to onStepSuccess and onStepFailure.
func (e *Engine) executeWithRetries(ctx context.Context, step dag.Step, workdir string, stepEnv []string) error {
	ex := e.execFn(step)
	if ex == nil {
		return fmt.Errorf("no executor for step %q", step.ID)
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
		e.writeEvent(state.Event{
			Type:    state.EventStepStarted,
			StepID:  step.ID,
			Attempt: attempt,
		})
		e.logger.Info("step started", "step", step.ID, "attempt", attempt)

		lastResult = ex.Run(stepCtx, step, e.runInfo.Dir, workdir, stepEnv)

		if lastResult.Err == nil {
			return e.onStepSuccess(ctx, step, lastResult, workdir, stepEnv, attempt)
		}

		if attempt < maxAttempts {
			e.writeEvent(state.Event{
				Type:    state.EventStepRetrying,
				StepID:  step.ID,
				Error:   lastResult.Err.Error(),
				Attempt: attempt,
			})
			e.logger.Warn("step failed, retrying", "step", step.ID, "attempt", attempt, "error", lastResult.Err)
			time.Sleep(retryDelay(attempt, step.Retry))
		}
	}

	return e.onStepFailure(ctx, step, lastResult, maxAttempts)
}

// onStepSuccess handles post-execution work after a successful step attempt:
// writing the completed event, collecting outputs, writing summary/meta/validation
// files, checking validations, verifying artifacts, saving cache, and running
// the on_success hook.
func (e *Engine) onStepSuccess(ctx context.Context, step dag.Step, result executor.Result, workdir string, _ []string, attempt int) error {
	e.writeEvent(state.Event{
		Type:     state.EventStepCompleted,
		StepID:   step.ID,
		ExitCode: result.ExitCode,
		Duration: result.Duration.String(),
		Attempt:  attempt,
	})
	e.logger.Info("step completed", "step", step.ID, "duration", result.Duration)

	// Collect outputs, namespaced by step ID
	e.collectOutputs(step.ID, result.Outputs)

	// Write summary, metadata, and validation files
	e.writeSummaryFile(step.ID, result.Summaries)
	e.writeMetadataFile(step.ID, result.Metadata)
	e.writeValidationFile(step.ID, result.Validations)

	// Check for validation failures: if error_on is "error" (or empty/default)
	// and any validation has status="fail", treat the step as failed.
	if step.ErrorOn == "" || step.ErrorOn == "error" {
		if err := checkValidationFailures(result.Validations); err != nil {
			e.writeEvent(state.Event{
				Type:     state.EventStepFailed,
				StepID:   step.ID,
				ExitCode: 0,
				Error:    err.Error(),
				Duration: result.Duration.String(),
				Attempt:  attempt,
			})
			e.logger.Error("step failed due to validation", "step", step.ID, "error", err)
			if step.OnFailure != nil {
				e.runHook(ctx, step.OnFailure, "step "+step.ID+" on_failure")
			}
			return err
		}
	}

	// Verify and record declared artifacts
	if err := e.verifyArtifacts(step, workdir); err != nil {
		return err
	}

	// Save to cache after successful execution
	if step.Cache && e.cacheStore != nil {
		cacheKey := e.computeCacheKey(step)
		entry := cache.Entry{
			RunID:   e.runInfo.ID,
			Outputs: result.Outputs,
		}
		if err := e.cacheStore.Save(e.dag.Name, step.ID, cacheKey, entry); err != nil {
			e.logger.Warn("failed to save cache", "step", step.ID, "error", err)
		}
	}

	// Run step-level on_success hook
	if step.OnSuccess != nil {
		e.runHook(ctx, step.OnSuccess, "step "+step.ID+" on_success")
	}

	return nil
}

// onStepFailure handles the final failure of a step after all retries are exhausted:
// writing the failed event and running the on_failure hook.
func (e *Engine) onStepFailure(ctx context.Context, step dag.Step, result executor.Result, maxAttempts int) error {
	e.writeEvent(state.Event{
		Type:        state.EventStepFailed,
		StepID:      step.ID,
		ExitCode:    result.ExitCode,
		Error:       result.Err.Error(),
		ErrorDetail: result.ErrorDetail,
		Duration:    result.Duration.String(),
		Attempt:     maxAttempts,
	})
	e.logger.Error("step failed", "step", step.ID, "error", result.Err)

	// Run step-level on_failure hook
	if step.OnFailure != nil {
		e.runHook(ctx, step.OnFailure, "step "+step.ID+" on_failure")
	}

	return result.Err
}

// verifyArtifacts checks that declared artifacts exist, computes hashes, and writes events.
func (e *Engine) verifyArtifacts(step dag.Step, workdir string) error {
	if len(step.Artifacts) == 0 {
		return nil
	}
	for _, art := range step.Artifacts {
		absPath := art.Path
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(workdir, art.Path)
		}

		// Handle versioned artifacts: rename file to include epoch timestamp
		if art.Versioned {
			ext := filepath.Ext(absPath)
			base := strings.TrimSuffix(absPath, ext)
			versionedPath := fmt.Sprintf("%s_%d%s", base, time.Now().Unix(), ext)
			if err := os.Rename(absPath, versionedPath); err != nil {
				return fmt.Errorf("step %q artifact %q: failed to version file: %w", step.ID, art.Name, err)
			}
			absPath = versionedPath
		}

		info, err := os.Stat(absPath)
		if err != nil {
			return fmt.Errorf("step %q artifact %q not found at %s", step.ID, art.Name, absPath)
		}

		hash, err := fileHash(absPath)
		if err != nil {
			return fmt.Errorf("step %q artifact %q: hash failed: %w", step.ID, art.Name, err)
		}

		// Recompute relative path (may have changed if versioned)
		relPath := art.Path
		if art.Versioned {
			relPath, _ = filepath.Rel(workdir, absPath)
		}

		e.writeEvent(state.Event{
			Type:   state.EventStepArtifact,
			StepID: step.ID,
			ArtifactInfo: &state.ArtifactInfo{
				ArtifactName:    art.Name,
				ArtifactPath:    relPath,
				ArtifactAbsPath: absPath,
				ArtifactHash:    hash,
				ArtifactSize:    info.Size(),
				ArtifactFormat:  art.Format,
			},
		})
		e.logger.Info("artifact recorded", "step", step.ID, "artifact", art.Name, "path", absPath, "size", info.Size())
	}
	return nil
}

// fileHash computes the SHA-256 hash of a file.
func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// computeCacheKey builds a deterministic cache key for a step based on its inputs.
func (e *Engine) computeCacheKey(step dag.Step) string {
	stepType := StepCacheType(step, e.dag)

	// Sorted env vars (DAG-level + step-level)
	var envVars []string
	for k, v := range e.dag.Env {
		envVars = append(envVars, k+"="+v.Value)
	}
	for k, v := range step.Env {
		envVars = append(envVars, k+"="+v.Value)
	}

	// Upstream outputs
	e.mu.Lock()
	outputs := make(map[string]string, len(e.outputs))
	for k, v := range e.outputs {
		outputs[k] = v
	}
	e.mu.Unlock()

	var renvLockHash string
	if e.meta != nil {
		renvLockHash = e.meta.RenvLockHash
	}

	return cache.ComputeStepKey(stepType, envVars, outputs, renvLockHash)
}

// StepCacheType returns the content identifier string used for cache key computation.
// It reads script file content when available.
func StepCacheType(step dag.Step, d *dag.DAG) string {
	switch {
	case step.Script != "":
		scriptPath := step.Script
		if !filepath.IsAbs(scriptPath) {
			workdir := d.ResolveWorkdir(step)
			scriptPath = filepath.Join(workdir, scriptPath)
		}
		data, err := os.ReadFile(scriptPath)
		if err == nil {
			return "script:" + string(data)
		}
		return "script:" + step.Script
	case step.RExpr != "":
		return "r_expr:" + step.RExpr
	case step.Command != "":
		return "command:" + step.Command
	default:
		return "type:" + dag.StepType(step)
	}
}

func (e *Engine) collectOutputs(stepID string, outputs map[string]string) {
	if len(outputs) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	prefix := "DAGGLE_OUTPUT_" + strings.ToUpper(strings.ReplaceAll(stepID, "-", "_")) + "_"
	for k, v := range outputs {
		key := prefix + strings.ToUpper(k)
		e.outputs[key] = v
		e.logger.Info("captured output", "step", stepID, "key", k, "value", v)
	}
}

// buildHookExtras computes additional env vars for hook execution.
func (e *Engine) buildHookExtras(runErr error) []string {
	var extras []string

	// DAGGLE_RUN_DURATION
	if e.meta != nil {
		dur := time.Since(e.meta.StartTime).Truncate(time.Second)
		extras = append(extras, "DAGGLE_RUN_DURATION="+dur.String())
	}

	// DAGGLE_RUN_STATUS
	if runErr == nil {
		extras = append(extras, "DAGGLE_RUN_STATUS=completed")
	} else {
		extras = append(extras, "DAGGLE_RUN_STATUS=failed")
	}

	// DAGGLE_FAILED_STEPS — comma-separated list of failed step IDs
	events, err := state.ReadEvents(e.runInfo.Dir)
	if err == nil {
		var failed []string
		for _, ev := range events {
			if ev.Type == state.EventStepFailed && ev.StepID != "" {
				failed = append(failed, ev.StepID)
			}
		}
		extras = append(extras, "DAGGLE_FAILED_STEPS="+strings.Join(failed, ","))
	}

	return extras
}

func (e *Engine) runHooks(ctx context.Context, runErr error) {
	// on_exit always runs
	if e.dag.OnExit != nil {
		e.runHook(ctx, e.dag.OnExit, "on_exit")
	}

	if runErr == nil && e.dag.OnSuccess != nil {
		e.runHook(ctx, e.dag.OnSuccess, "on_success")
	}
	if runErr != nil && e.dag.OnFailure != nil {
		e.runHook(ctx, e.dag.OnFailure, "on_failure")
	}
}

func (e *Engine) runHook(ctx context.Context, hook *dag.Hook, name string) {
	e.logger.Info("running hook", "hook", name)

	var cmd *exec.Cmd
	switch {
	case hook.RExpr != "":
		// Write to temp file and run via Rscript
		tmpFile := filepath.Join(e.runInfo.Dir, "hook_"+sanitize(name)+".R")
		if err := os.WriteFile(tmpFile, []byte(hook.RExpr), 0o644); err != nil {
			e.logger.Error("hook write failed", "hook", name, "error", err)
			return
		}
		defer func() { _ = os.Remove(tmpFile) }()
		cmd = exec.CommandContext(ctx, state.ToolPath("rscript"), "--no-save", "--no-restore", tmpFile)
	case hook.Command != "":
		cmd = exec.CommandContext(ctx, state.ToolPath("sh"), "-c", hook.Command)
	default:
		return
	}

	// Set working directory (empty step falls back to DAG workdir or SourceDir)
	cmd.Dir = e.dag.ResolveWorkdir(dag.Step{})

	// Provide env with run metadata + renv + outputs + hook extras
	baseEnv := buildEnv(e.dag, e.runInfo, e.renvLibPath)
	cmd.Env = append(os.Environ(), e.buildStepEnv(baseEnv, dag.Step{})...)
	cmd.Env = append(cmd.Env, e.hookEnvExtras...)

	// Route hook output through log files so secrets can be redacted
	hookStdout, err := os.Create(filepath.Join(e.runInfo.Dir, "hook_"+sanitize(name)+".stdout.log"))
	if err != nil {
		e.logger.Error("hook log create failed", "hook", name, "error", err)
		return
	}
	defer func() { _ = hookStdout.Close() }()
	hookStderr, err := os.Create(filepath.Join(e.runInfo.Dir, "hook_"+sanitize(name)+".stderr.log"))
	if err != nil {
		e.logger.Error("hook log create failed", "hook", name, "error", err)
		return
	}
	defer func() { _ = hookStderr.Close() }()
	cmd.Stdout = hookStdout
	cmd.Stderr = hookStderr

	if err := cmd.Run(); err != nil {
		e.logger.Error("hook failed", "hook", name, "error", err)
	}
}

// evaluateCondition runs a when/precondition check.
// Returns (shouldSkip, error). shouldSkip is true if the command exits non-zero (condition not met).
func (e *Engine) evaluateCondition(ctx context.Context, cond *dag.StepCondition, env []string) (bool, error) {
	var cmd *exec.Cmd
	switch {
	case cond.RExpr != "":
		tmpFile := filepath.Join(e.runInfo.Dir, "condition_"+sanitize(cond.RExpr[:min(len(cond.RExpr), 20)])+".R")
		if err := os.WriteFile(tmpFile, []byte(fmt.Sprintf("if (!(%s)) quit(status = 1, save = 'no')\n", cond.RExpr)), 0o644); err != nil {
			return false, err
		}
		defer func() { _ = os.Remove(tmpFile) }()
		cmd = exec.CommandContext(ctx, state.ToolPath("rscript"), "--no-save", "--no-restore", tmpFile)
	case cond.Command != "":
		cmd = exec.CommandContext(ctx, state.ToolPath("sh"), "-c", cond.Command)
	default:
		return false, nil
	}

	cmd.Env = append(os.Environ(), env...)
	cmd.Dir = e.dag.ResolveWorkdir(dag.Step{})

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return true, nil // condition not met, skip
		}
		return false, err // actual error
	}
	return false, nil // condition met, don't skip
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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

// writeSummaryFile writes concatenated summary content to {step.ID}.summary.md.
func (e *Engine) writeSummaryFile(stepID string, summaries []executor.Summary) {
	if len(summaries) == 0 {
		return
	}
	var parts []string
	for _, s := range summaries {
		parts = append(parts, s.Content)
	}
	path := filepath.Join(e.runInfo.Dir, stepID+".summary.md")
	if err := os.WriteFile(path, []byte(strings.Join(parts, "\n")), 0o644); err != nil {
		e.logger.Warn("failed to write summary file", "step", stepID, "error", err)
	}
}

// writeMetadataFile writes metadata entries as a JSON array to {step.ID}.meta.json.
func (e *Engine) writeMetadataFile(stepID string, metadata []executor.MetaEntry) {
	if len(metadata) == 0 {
		return
	}
	type metaJSON struct {
		Name  string `json:"name"`
		Type  string `json:"type"`
		Value string `json:"value"`
	}
	entries := make([]metaJSON, len(metadata))
	for i, m := range metadata {
		entries[i] = metaJSON{Name: m.Name, Type: m.Type, Value: m.Value}
	}
	data, err := json.Marshal(entries)
	if err != nil {
		e.logger.Warn("failed to marshal metadata", "step", stepID, "error", err)
		return
	}
	path := filepath.Join(e.runInfo.Dir, stepID+".meta.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		e.logger.Warn("failed to write metadata file", "step", stepID, "error", err)
	}
}

// writeValidationFile writes validation results as a JSON array to {step.ID}.validations.json.
func (e *Engine) writeValidationFile(stepID string, validations []executor.ValidationResult) {
	if len(validations) == 0 {
		return
	}
	type validationJSON struct {
		Name    string `json:"name"`
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	entries := make([]validationJSON, len(validations))
	for i, v := range validations {
		entries[i] = validationJSON{Name: v.Name, Status: v.Status, Message: v.Message}
	}
	data, err := json.Marshal(entries)
	if err != nil {
		e.logger.Warn("failed to marshal validations", "step", stepID, "error", err)
		return
	}
	path := filepath.Join(e.runInfo.Dir, stepID+".validations.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		e.logger.Warn("failed to write validations file", "step", stepID, "error", err)
	}
}

// checkValidationFailures returns an error if any validation result has status "fail".
func checkValidationFailures(validations []executor.ValidationResult) error {
	var failed []string
	for _, v := range validations {
		if v.Status == "fail" {
			failed = append(failed, v.Name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("validation failed: %s", strings.Join(failed, ", "))
	}
	return nil
}

func retryDelay(attempt int, retry *dag.Retry) time.Duration {
	if retry == nil {
		return time.Duration(attempt) * time.Second
	}
	var delay time.Duration
	switch retry.Backoff {
	case "exponential":
		delay = time.Duration(1<<uint(attempt-1)) * time.Second
	default:
		delay = time.Duration(attempt) * time.Second
	}
	if retry.MaxDelay != "" {
		if max, err := time.ParseDuration(retry.MaxDelay); err == nil && delay > max {
			delay = max
		}
	}
	return delay
}
