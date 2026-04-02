package scheduler

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/robfig/cron/v3"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/engine"
	"github.com/cynkra/daggle/executor"
	"github.com/cynkra/daggle/state"
)

const (
	defaultPollInterval  = 30 * time.Second
	defaultMaxConcurrent = 4
	shutdownGracePeriod  = 5 * time.Minute
	defaultDebounce      = 500 * time.Millisecond
)

// dagEntry tracks a registered DAG and its active triggers.
type dagEntry struct {
	cronID   cron.EntryID       // zero if no cron trigger
	schedule string             // cron expression (empty if none)
	hash     string             // file content hash for change detection
	cancelFns []context.CancelFunc // cancel functions for non-cron trigger goroutines
}

// teardown cancels all non-cron triggers for this entry.
func (e *dagEntry) teardown() {
	for _, cancel := range e.cancelFns {
		cancel()
	}
	e.cancelFns = nil
}

// Scheduler manages trigger-based DAG execution.
type Scheduler struct {
	cron   *cron.Cron
	dagDir string
	logger *slog.Logger

	mu            sync.Mutex
	registered    map[string]*dagEntry          // dagName -> entry
	running       map[string]context.CancelFunc // dagName -> cancel
	runningCount  int
	maxConcurrent int
}

// New creates a new Scheduler that watches the given DAG directory.
func New(dagDir string) *Scheduler {
	return &Scheduler{
		cron:          cron.New(),
		dagDir:        dagDir,
		logger:        slog.Default(),
		registered:    make(map[string]*dagEntry),
		running:       make(map[string]context.CancelFunc),
		maxConcurrent: defaultMaxConcurrent,
	}
}

// Start begins the scheduler loop. It blocks until ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) error {
	// Initial scan
	if err := s.syncDAGs(ctx); err != nil {
		s.logger.Error("initial DAG scan failed", "error", err)
	}

	s.cron.Start()
	s.logger.Info("scheduler started", "dag_dir", s.dagDir)

	// Poll for DAG file changes
	ticker := time.NewTicker(defaultPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("scheduler stopping")
			s.shutdown()
			return nil
		case <-ticker.C:
			if err := s.syncDAGs(ctx); err != nil {
				s.logger.Error("DAG sync failed", "error", err)
			}
		}
	}
}

// shutdown performs graceful shutdown.
func (s *Scheduler) shutdown() {
	// Stop cron (no new triggers)
	cronCtx := s.cron.Stop()

	// Cancel all non-cron trigger goroutines
	s.mu.Lock()
	for _, entry := range s.registered {
		entry.teardown()
	}
	s.mu.Unlock()

	// Wait for cron jobs to finish their scheduling
	<-cronCtx.Done()

	// Wait for in-flight runs with a grace period
	s.mu.Lock()
	runCount := len(s.running)
	s.mu.Unlock()

	if runCount > 0 {
		s.logger.Info("waiting for in-flight runs", "count", runCount)
		deadline := time.After(shutdownGracePeriod)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-deadline:
				s.logger.Warn("grace period expired, cancelling remaining runs")
				s.mu.Lock()
				for name, cancel := range s.running {
					s.logger.Warn("cancelling run", "dag", name)
					cancel()
				}
				s.mu.Unlock()
				// Give processes a moment to clean up
				time.Sleep(2 * time.Second)
				return
			case <-ticker.C:
				s.mu.Lock()
				remaining := len(s.running)
				s.mu.Unlock()
				if remaining == 0 {
					s.logger.Info("all runs completed")
					return
				}
			}
		}
	}
}

// syncDAGs scans the DAG directory and updates trigger registrations.
func (s *Scheduler) syncDAGs(ctx context.Context) error {
	entries, err := os.ReadDir(s.dagDir)
	if err != nil {
		return fmt.Errorf("read DAG dir: %w", err)
	}

	seen := make(map[string]bool)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}

		path := filepath.Join(s.dagDir, name)
		hash, err := fileHash(path)
		if err != nil {
			s.logger.Warn("failed to hash DAG file", "path", path, "error", err)
			continue
		}

		d, err := dag.ParseFile(path)
		if err != nil {
			s.logger.Warn("failed to parse DAG", "path", path, "error", err)
			continue
		}

		if !d.HasTrigger() {
			continue
		}

		seen[d.Name] = true

		s.mu.Lock()
		existing, exists := s.registered[d.Name]
		s.mu.Unlock()

		if exists && existing.hash == hash {
			continue // no change
		}

		// Tear down old triggers
		if exists {
			s.cron.Remove(existing.cronID)
			existing.teardown()
			s.logger.Info("updating DAG triggers", "dag", d.Name)
		} else {
			s.logger.Info("registering DAG", "dag", d.Name)
		}

		newEntry := &dagEntry{hash: hash}
		dagPath := path // capture for closure

		// Set up cron trigger
		if sched := d.CronSchedule(); sched != "" {
			entryID, err := s.cron.AddFunc(sched, func() {
				s.triggerRun(dagPath, "cron")
			})
			if err != nil {
				s.logger.Error("invalid cron schedule", "dag", d.Name, "schedule", sched, "error", err)
			} else {
				newEntry.cronID = entryID
				newEntry.schedule = sched
			}
		}

		// Set up watch trigger
		if d.Trigger.Watch != nil {
			cancelFn := s.setupWatchTrigger(ctx, d, dagPath)
			if cancelFn != nil {
				newEntry.cancelFns = append(newEntry.cancelFns, cancelFn)
			}
		}

		s.mu.Lock()
		s.registered[d.Name] = newEntry
		s.mu.Unlock()
	}

	// Remove DAGs that no longer exist
	s.mu.Lock()
	for name, entry := range s.registered {
		if !seen[name] {
			s.cron.Remove(entry.cronID)
			entry.teardown()
			delete(s.registered, name)
			s.logger.Info("unregistered DAG", "dag", name)
		}
	}
	s.mu.Unlock()

	return nil
}

// setupWatchTrigger starts a file watcher goroutine for the DAG's watch trigger.
// Returns a cancel function to stop the watcher, or nil on error.
func (s *Scheduler) setupWatchTrigger(ctx context.Context, d *dag.DAG, dagPath string) context.CancelFunc {
	w := d.Trigger.Watch

	// Resolve watch path relative to DAG directory
	watchPath := w.Path
	if !filepath.IsAbs(watchPath) {
		watchPath = filepath.Join(d.SourceDir, watchPath)
	}

	// Parse debounce duration
	debounce := defaultDebounce
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

// triggerRun executes a DAG run. Called by any trigger source.
func (s *Scheduler) triggerRun(dagPath string, source string) {
	d, err := dag.ParseFile(dagPath)
	if err != nil {
		s.logger.Error("failed to parse DAG for run", "path", dagPath, "error", err)
		return
	}

	s.mu.Lock()

	// Skip if already running (overlap policy: skip)
	if _, running := s.running[d.Name]; running {
		s.mu.Unlock()
		s.logger.Info("skipping run, DAG already active", "dag", d.Name, "source", source)
		return
	}

	// Check max concurrent
	if s.runningCount >= s.maxConcurrent {
		s.mu.Unlock()
		s.logger.Warn("skipping run, max concurrent DAGs reached", "dag", d.Name, "max", s.maxConcurrent)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.running[d.Name] = cancel
	s.runningCount++
	s.mu.Unlock()

	// Run in a goroutine
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.running, d.Name)
			s.runningCount--
			s.mu.Unlock()
		}()

		s.logger.Info("triggered run starting", "dag", d.Name, "source", source)

		expanded, err := dag.ExpandDAG(d, nil)
		if err != nil {
			s.logger.Error("template expansion failed", "dag", d.Name, "error", err)
			return
		}

		run, err := state.CreateRun(expanded.Name)
		if err != nil {
			s.logger.Error("create run failed", "dag", d.Name, "error", err)
			return
		}

		eng := engine.New(expanded, run, executor.New)

		if err := eng.Run(ctx); err != nil {
			s.logger.Error("triggered run failed", "dag", d.Name, "run_id", run.ID, "source", source, "error", err)
		} else {
			s.logger.Info("triggered run completed", "dag", d.Name, "run_id", run.ID, "source", source)
		}
	}()
}

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
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
