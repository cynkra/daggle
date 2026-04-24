package scheduler

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
)

// setupGitTrigger starts a polling goroutine that checks for new commits or tags
// and triggers a run when changes are detected.
func (s *Scheduler) setupGitTrigger(ctx context.Context, d *dag.DAG, dagPath string) context.CancelFunc {
	g := d.Trigger.Git

	pollInterval := 30 * time.Second
	if g.PollInterval != "" {
		if parsed, err := time.ParseDuration(g.PollInterval); err == nil {
			pollInterval = parsed
		}
	}

	// Resolve git repo directory (use DAG source dir)
	repoDir := d.SourceDir
	if repoDir == "" {
		repoDir = filepath.Dir(dagPath)
	}

	branch := g.Branch
	if branch == "" {
		branch = "HEAD"
	}

	gitCtx, cancel := context.WithCancel(ctx)

	s.logger.Info("git trigger started", "dag", d.Name, "branch", branch, "poll_interval", pollInterval)

	go func() {
		var lastHash string

		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-gitCtx.Done():
				return
			case <-ticker.C:
				// Get current commit hash
				cmd := exec.CommandContext(gitCtx, state.ToolPath("git"), "-C", repoDir, "rev-parse", branch)
				out, err := cmd.Output()
				if err != nil {
					continue
				}
				currentHash := strings.TrimSpace(string(out))

				if lastHash == "" {
					lastHash = currentHash
					continue
				}

				if currentHash != lastHash {
					s.logger.Info("git change detected, triggering run", "dag", d.Name, "branch", branch)
					lastHash = currentHash
					s.triggerRun(dagPath, "git")
				}
			}
		}
	}()

	return cancel
}
