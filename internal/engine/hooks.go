package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/internal/notify"
	"github.com/cynkra/daggle/state"
)

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

	// Notification-only hook: dispatch via configured channel, no subprocess.
	if hook.Notify != "" {
		e.dispatchNotify(hook, name)
		return
	}

	var cmd *exec.Cmd
	switch {
	case hook.RExpr != "":
		// Write to temp file and run via Rscript
		tmpFile := filepath.Join(e.runInfo.Dir, "hook_"+sanitize(name)+".R")
		if err := os.WriteFile(tmpFile, []byte(hook.RExpr), 0o644); err != nil {
			e.logger.Error("hook write failed", "hook", name, "error", e.redactErr(err))
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
		e.logger.Error("hook log create failed", "hook", name, "error", e.redactErr(err))
		return
	}
	defer func() { _ = hookStdout.Close() }()
	hookStderr, err := os.Create(filepath.Join(e.runInfo.Dir, "hook_"+sanitize(name)+".stderr.log"))
	if err != nil {
		e.logger.Error("hook log create failed", "hook", name, "error", e.redactErr(err))
		return
	}
	defer func() { _ = hookStderr.Close() }()
	cmd.Stdout = hookStdout
	cmd.Stderr = hookStderr

	if err := cmd.Run(); err != nil {
		e.logger.Error("hook failed", "hook", name, "error", e.redactErr(err))
	}
}

// dispatchNotify sends a notification through a configured channel.
// Channel existence failures are logged (not fatal) to match other hook semantics.
func (e *Engine) dispatchNotify(hook *dag.Hook, hookName string) {
	ch, ok := e.notifications[hook.Notify]
	if !ok {
		e.logger.Error("notify hook references unknown channel", "hook", hookName, "channel", hook.Notify)
		return
	}
	msg := hook.Message
	if msg == "" {
		status := "completed"
		if strings.Contains(hookName, "failure") {
			status = "failed"
		}
		msg = fmt.Sprintf("DAG %q %s (run %s)", e.dag.Name, status, e.runInfo.ID)
	}
	cfg := state.Config{Notifications: map[string]state.NotificationChannel{hook.Notify: ch}}
	if err := notify.Send(cfg, hook.Notify, msg); err != nil {
		e.logger.Error("notify hook failed", "hook", hookName, "channel", hook.Notify, "error", e.redactErr(err))
		return
	}
	e.logger.Info("notify hook sent", "hook", hookName, "channel", hook.Notify)
}
