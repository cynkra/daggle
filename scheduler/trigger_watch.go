package scheduler

import (
	"context"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/cynkra/daggle/dag"
)

// setupWatchTrigger starts a file watcher goroutine for the DAG's watch trigger.
// Returns a cancel function to stop the watcher, or nil on error.
func (s *Scheduler) setupWatchTrigger(ctx context.Context, d *dag.DAG, dagPath string) context.CancelFunc {
	w := d.Trigger.Watch

	// Resolve watch path relative to DAG directory
	watchPath := w.Path
	if !filepath.IsAbs(watchPath) {
		watchPath = filepath.Join(d.SourceDir, watchPath)
	}

	// Parse debounce duration (per-DAG override, then global config, then default)
	debounce := s.watchDebounce
	if w.Debounce != "" {
		if parsed, err := time.ParseDuration(w.Debounce); err == nil {
			debounce = parsed
		}
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		s.logger.Error("failed to create file watcher", "dag", d.Name, "error", err)
		return nil
	}

	if err := watcher.Add(watchPath); err != nil {
		s.logger.Error("failed to watch path", "dag", d.Name, "path", watchPath, "error", err)
		_ = watcher.Close()
		return nil
	}

	watchCtx, cancel := context.WithCancel(ctx)
	s.logger.Info("watching directory", "dag", d.Name, "path", watchPath, "pattern", w.Pattern, "debounce", debounce)

	go func() {
		defer func() { _ = watcher.Close() }()

		var debounceTimer *time.Timer
		var debounceC <-chan time.Time

		for {
			select {
			case <-watchCtx.Done():
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				return

			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Only care about create and write events
				if !event.Has(fsnotify.Create) && !event.Has(fsnotify.Write) {
					continue
				}
				// Filter by pattern if set
				if w.Pattern != "" {
					matched, _ := filepath.Match(w.Pattern, filepath.Base(event.Name))
					if !matched {
						continue
					}
				}
				// Reset debounce timer
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.NewTimer(debounce)
				debounceC = debounceTimer.C

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				s.logger.Warn("file watcher error", "dag", d.Name, "error", err)

			case <-debounceC:
				s.logger.Info("file change detected, triggering run", "dag", d.Name)
				s.triggerRun(dagPath, "watch")
				debounceC = nil
			}
		}
	}()

	return cancel
}
