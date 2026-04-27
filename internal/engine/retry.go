package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/cynkra/daggle/cache"
	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/internal/executor"
	"github.com/cynkra/daggle/state"
)

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
			e.logger.Warn("step failed, retrying", "step", step.ID, "attempt", attempt, "error", e.redactErr(lastResult.Err))
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
		Type:       state.EventStepCompleted,
		StepID:     step.ID,
		ExitCode:   result.ExitCode,
		Duration:   result.Duration.String(),
		Attempt:    attempt,
		PeakRSSKB:  result.PeakRSSKB,
		UserCPUSec: result.UserCPUSec,
		SysCPUSec:  result.SysCPUSec,
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
			e.logger.Error("step failed due to validation", "step", step.ID, "error", e.redactErr(err))
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
			e.logger.Warn("failed to save cache", "step", step.ID, "error", e.redactErr(err))
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
	e.logger.Error("step failed", "step", step.ID, "error", e.redactErr(result.Err))

	// Run step-level on_failure hook
	if step.OnFailure != nil {
		e.runHook(ctx, step.OnFailure, "step "+step.ID+" on_failure")
	}

	return result.Err
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
