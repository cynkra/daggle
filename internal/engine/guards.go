package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/internal/envutil"
	"github.com/cynkra/daggle/state"
)

// EventStepSkipped is emitted when a step is skipped due to a `when` condition.
const eventStepSkipped = "step_skipped"

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

	cmd.Env = envutil.WithUTF8Locale(append(os.Environ(), env...))
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
