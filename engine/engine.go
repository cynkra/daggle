package engine

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/executor"
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

	// redactor replaces secret values in event error messages.
	redactor *dag.Redactor

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

	// Run lifecycle hooks
	e.runHooks(ctx, runErr)

	return runErr
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

// EventStepSkipped is emitted when a step is skipped due to a `when` condition.
const eventStepSkipped = "step_skipped"

func (e *Engine) runStep(ctx context.Context, step dag.Step, env []string) error {
	// Check `when` condition — skip (not fail) if false
	if step.When != nil {
		if skip, err := e.evaluateCondition(ctx, step.When, env); err != nil {
			return fmt.Errorf("step %q when condition failed: %w", step.ID, err)
		} else if skip {
			e.writeEvent(state.Event{Type: eventStepSkipped, StepID: step.ID})
			e.logger.Info("step skipped (condition not met)", "step", step.ID)
			return nil
		}
	}

	// Check preconditions — fail if not met
	for i, pre := range step.Preconditions {
		cond := &dag.StepCondition{RExpr: pre.RExpr, Command: pre.Command}
		if fail, err := e.evaluateCondition(ctx, cond, env); err != nil {
			return fmt.Errorf("step %q precondition %d failed: %w", step.ID, i+1, err)
		} else if fail {
			return fmt.Errorf("step %q precondition %d not met", step.ID, i+1)
		}
	}

	ex := e.execFn(step)
	if ex == nil {
		return fmt.Errorf("no executor for step %q", step.ID)
	}

	// Resolve working directory
	workdir := e.dag.ResolveWorkdir(step)

	// Merge step-level env + accumulated outputs from prior steps
	stepEnv := make([]string, len(env))
	copy(stepEnv, env)
	for k, v := range step.Env {
		stepEnv = append(stepEnv, k+"="+v.Value)
	}
	e.mu.Lock()
	for k, v := range e.outputs {
		stepEnv = append(stepEnv, k+"="+v)
	}
	e.mu.Unlock()

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
			e.writeEvent(state.Event{
				Type:     state.EventStepCompleted,
				StepID:   step.ID,
				ExitCode: lastResult.ExitCode,
				Duration: lastResult.Duration.String(),
				Attempt:  attempt,
			})
			e.logger.Info("step completed", "step", step.ID, "duration", lastResult.Duration)

			// Collect outputs, namespaced by step ID
			e.collectOutputs(step.ID, lastResult.Outputs)

			// Run step-level on_success hook
			if step.OnSuccess != nil {
				e.runHook(ctx, step.OnSuccess, "step "+step.ID+" on_success")
			}

			return nil
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

	e.writeEvent(state.Event{
		Type:        state.EventStepFailed,
		StepID:      step.ID,
		ExitCode:    lastResult.ExitCode,
		Error:       lastResult.Err.Error(),
		ErrorDetail: lastResult.ErrorDetail,
		Duration:    lastResult.Duration.String(),
		Attempt:     maxAttempts,
	})
	e.logger.Error("step failed", "step", step.ID, "error", lastResult.Err)

	// Run step-level on_failure hook
	if step.OnFailure != nil {
		e.runHook(ctx, step.OnFailure, "step "+step.ID+" on_failure")
	}

	return lastResult.Err
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
		if err := os.WriteFile(tmpFile, []byte(hook.RExpr), 0644); err != nil {
			e.logger.Error("hook write failed", "hook", name, "error", err)
			return
		}
		defer os.Remove(tmpFile)
		cmd = exec.CommandContext(ctx, "Rscript", "--no-save", "--no-restore", tmpFile)
	case hook.Command != "":
		cmd = exec.CommandContext(ctx, "sh", "-c", hook.Command)
	default:
		return
	}

	// Set working directory
	if e.dag.Workdir != "" {
		cmd.Dir = e.dag.Workdir
	} else if e.dag.SourceDir != "" {
		cmd.Dir = e.dag.SourceDir
	}

	// Provide env with run metadata + renv + outputs
	cmd.Env = append(os.Environ(), buildEnv(e.dag, e.runInfo, e.renvLibPath)...)
	e.mu.Lock()
	for k, v := range e.outputs {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	e.mu.Unlock()

	// Route hook output through log files so secrets can be redacted
	hookStdout, err := os.Create(filepath.Join(e.runInfo.Dir, "hook_"+sanitize(name)+".stdout.log"))
	if err != nil {
		e.logger.Error("hook log create failed", "hook", name, "error", err)
		return
	}
	defer hookStdout.Close()
	hookStderr, err := os.Create(filepath.Join(e.runInfo.Dir, "hook_"+sanitize(name)+".stderr.log"))
	if err != nil {
		e.logger.Error("hook log create failed", "hook", name, "error", err)
		return
	}
	defer hookStderr.Close()
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
		if err := os.WriteFile(tmpFile, []byte(fmt.Sprintf("if (!(%s)) quit(status = 1, save = 'no')\n", cond.RExpr)), 0644); err != nil {
			return false, err
		}
		defer os.Remove(tmpFile)
		cmd = exec.CommandContext(ctx, "Rscript", "--no-save", "--no-restore", tmpFile)
	case cond.Command != "":
		cmd = exec.CommandContext(ctx, "sh", "-c", cond.Command)
	default:
		return false, nil
	}

	cmd.Env = append(os.Environ(), env...)
	if e.dag.Workdir != "" {
		cmd.Dir = e.dag.Workdir
	} else if e.dag.SourceDir != "" {
		cmd.Dir = e.dag.SourceDir
	}

	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); ok {
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
