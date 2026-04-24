package scheduler

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
)

// checkDeadlines scans all DAG sources for DAGs with deadlines and fires
// the on_deadline hook if the deadline has passed today without a run starting.
func (s *Scheduler) checkDeadlines(ctx context.Context) {
	s.mu.Lock()
	sources := make([]state.DAGSource, len(s.sources))
	copy(sources, s.sources)
	s.mu.Unlock()

	now := time.Now()
	today := now.Format("2006-01-02")

	for _, src := range sources {
		entries, err := os.ReadDir(src.Dir)
		if err != nil {
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
			path := filepath.Join(src.Dir, name)
			d, err := dag.ParseFile(path)
			if err != nil {
				continue
			}
			if d.Trigger == nil || d.Trigger.Deadline == "" || d.Trigger.OnDeadline == nil {
				continue
			}

			// Check if already fired today
			s.mu.Lock()
			firedDate := s.deadlineFired[d.Name]
			s.mu.Unlock()
			if firedDate == today {
				continue
			}

			// Parse deadline time
			deadlineTime, err := time.Parse("2006-01-02 15:04", today+" "+d.Trigger.Deadline)
			if err != nil {
				s.logger.Warn("invalid deadline time", "dag", d.Name, "deadline", d.Trigger.Deadline, "error", err)
				continue
			}

			if now.Before(deadlineTime) {
				continue
			}

			// Check if DAG has had a run start today
			if s.hasRunToday(d.Name, today) {
				continue
			}

			// Fire the deadline hook
			s.mu.Lock()
			s.deadlineFired[d.Name] = today
			s.mu.Unlock()

			s.logger.Warn("deadline missed, firing on_deadline hook", "dag", d.Name, "deadline", d.Trigger.Deadline)
			s.fireDeadlineHook(ctx, d.Name, d.Trigger.OnDeadline)
		}
	}
}

// hasRunToday checks whether a DAG has any run directory for the given date.
func (s *Scheduler) hasRunToday(dagName, today string) bool {
	dayDir := filepath.Join(state.RunsDir(), dagName, today)
	entries, err := os.ReadDir(dayDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "run_") {
			return true
		}
	}
	return false
}

// fireDeadlineHook executes a deadline hook (R expression or shell command).
func (s *Scheduler) fireDeadlineHook(ctx context.Context, dagName string, hook *dag.Hook) {
	var cmd *exec.Cmd
	switch {
	case hook.RExpr != "":
		cmd = exec.CommandContext(ctx, state.ToolPath("rscript"), "-e", hook.RExpr)
	case hook.Command != "":
		cmd = exec.CommandContext(ctx, state.ToolPath("sh"), "-c", hook.Command)
	default:
		return
	}

	if err := cmd.Run(); err != nil {
		s.logger.Error("deadline hook failed", "dag", dagName, "error", err)
	} else {
		s.logger.Info("deadline hook completed", "dag", dagName)
	}
}
