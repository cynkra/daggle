package scheduler

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
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

// dagCompletionEvent is emitted when a DAG run finishes.
type dagCompletionEvent struct {
	DAGName string
	Status  string // "completed" or "failed"
}

// onDAGListener tracks a DAG that should be triggered when another DAG completes.
type onDAGListener struct {
	dagPath string // path to the downstream DAG file
	status  string // "completed", "failed", or "any"
}

// runEntry tracks an active DAG run.
type runEntry struct {
	cancel context.CancelFunc
	done   chan struct{} // closed when the run goroutine exits
}

// dagEntry tracks a registered DAG and its active triggers.
type dagEntry struct {
	cronID    cron.EntryID         // zero if no cron trigger
	schedule  string               // cron expression (empty if none)
	hash      string               // file content hash for change detection
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
	cron    *cron.Cron
	sources []state.DAGSource
	logger  *slog.Logger

	mu             sync.Mutex
	registered     map[string]*dagEntry              // dagName -> entry
	running        map[string]*runEntry              // dagName -> active run
	runningCount   int
	maxConcurrent  int
	onDAGListeners map[string][]onDAGListener        // upstreamDAGName -> downstream listeners
	completions    chan dagCompletionEvent
	webhooks       map[string]webhookEntry            // dagName -> webhook config
	webhookCloseFn func()                             // close the webhook server
	webhookAddr    string                             // address the webhook server is listening on
	ctx            context.Context                    // scheduler lifecycle context
	deadlineFired  map[string]string                  // dagName -> date string (YYYY-MM-DD) of last fired deadline
}

// New creates a new Scheduler that watches the given DAG sources.
func New(sources []state.DAGSource) *Scheduler {
	return &Scheduler{
		cron:           cron.New(),
		sources:        sources,
		logger:         slog.Default(),
		registered:     make(map[string]*dagEntry),
		running:        make(map[string]*runEntry),
		maxConcurrent:  defaultMaxConcurrent,
		onDAGListeners: make(map[string][]onDAGListener),
		completions:    make(chan dagCompletionEvent, 256),
		webhooks:       make(map[string]webhookEntry),
		deadlineFired:  make(map[string]string),
	}
}

// Status holds queryable scheduler state for the health endpoint.
type Status struct {
	RegisteredDAGs int
	ActiveRuns     int
	MaxConcurrent  int
	TriggerCounts  map[string]int // trigger type -> count (e.g. "schedule": 3)
}

// Status returns a snapshot of the scheduler's current state.
func (s *Scheduler) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()

	triggers := make(map[string]int)
	for _, entry := range s.registered {
		if entry.schedule != "" {
			triggers["schedule"]++
		}
		triggers["other"] += len(entry.cancelFns)
	}
	for name := range s.webhooks {
		_ = name
		triggers["webhook"]++
	}
	for _, listeners := range s.onDAGListeners {
		triggers["on_dag"] += len(listeners)
	}
	// Remove "other" if zero
	if triggers["other"] == 0 {
		delete(triggers, "other")
	}

	return Status{
		RegisteredDAGs: len(s.registered),
		ActiveRuns:     s.runningCount,
		MaxConcurrent:  s.maxConcurrent,
		TriggerCounts:  triggers,
	}
}

// Reload triggers an immediate rescan of DAG sources. If newSources is
// non-empty, the scheduler's source list is replaced before scanning,
// allowing newly registered projects to be picked up without a restart.
func (s *Scheduler) Reload(ctx context.Context, newSources []state.DAGSource) {
	if len(newSources) > 0 {
		s.mu.Lock()
		s.sources = newSources
		s.mu.Unlock()
		s.logger.Info("DAG sources updated", "count", len(newSources))
	}
	if err := s.syncDAGs(ctx); err != nil {
		s.logger.Error("reload failed", "error", err)
	} else {
		s.logger.Info("DAGs reloaded")
	}
}

// Start begins the scheduler loop. It blocks until ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) error {
	s.ctx = ctx

	// Initial scan
	if err := s.syncDAGs(ctx); err != nil {
		s.logger.Error("initial DAG scan failed", "error", err)
	}

	s.cron.Start()
	s.logger.Info("scheduler started", "sources", len(s.sources))

	// Start on_dag completion dispatcher
	go s.dispatchCompletions(ctx)

	// Start auto-cleanup if configured
	cfg, err := state.LoadConfig()
	if err != nil {
		s.logger.Warn("failed to load config, skipping auto-cleanup", "error", err)
	}
	var cleanupTicker *time.Ticker
	var cleanupThreshold time.Duration
	if cfg.Cleanup != nil && cfg.Cleanup.OlderThan != "" {
		cleanupThreshold, err = state.ParseDurationWithDays(cfg.Cleanup.OlderThan)
		if err != nil {
			s.logger.Warn("invalid cleanup.older_than, skipping auto-cleanup", "error", err)
		} else {
			interval := time.Hour // default
			if cfg.Cleanup.Interval != "" {
				if parsed, err := state.ParseDurationWithDays(cfg.Cleanup.Interval); err == nil {
					interval = parsed
				}
			}
			cleanupTicker = time.NewTicker(interval)
			s.logger.Info("auto-cleanup enabled", "older_than", cfg.Cleanup.OlderThan, "interval", interval)
		}
	}
	if cleanupTicker != nil {
		defer cleanupTicker.Stop()
	}

	// Poll for DAG file changes
	ticker := time.NewTicker(defaultPollInterval)
	defer ticker.Stop()

	for {
		// Build cleanup channel (nil if disabled, which means select never fires)
		var cleanupCh <-chan time.Time
		if cleanupTicker != nil {
			cleanupCh = cleanupTicker.C
		}

		select {
		case <-ctx.Done():
			s.logger.Info("scheduler stopping")
			s.shutdown()
			return nil
		case <-ticker.C:
			if err := s.syncDAGs(ctx); err != nil {
				s.logger.Error("DAG sync failed", "error", err)
			}
			s.checkDeadlines(ctx)
		case <-cleanupCh:
			result, err := state.CleanupRuns(cleanupThreshold)
			if err != nil {
				s.logger.Error("auto-cleanup failed", "error", err)
			} else if result.Removed > 0 {
				s.logger.Info("auto-cleanup completed", "removed", result.Removed, "freed_bytes", result.FreedBytes)
			}
		}
	}
}

// shutdown performs graceful shutdown.
func (s *Scheduler) shutdown() {
	// Stop cron (no new triggers)
	cronCtx := s.cron.Stop()

	// Cancel all non-cron trigger goroutines and stop webhook server
	s.mu.Lock()
	for _, entry := range s.registered {
		entry.teardown()
	}
	if s.webhookCloseFn != nil {
		s.webhookCloseFn()
		s.webhookCloseFn = nil
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
				for name, entry := range s.running {
					s.logger.Warn("cancelling run", "dag", name)
					entry.cancel()
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

// syncDAGs scans all DAG source directories and updates trigger registrations.
func (s *Scheduler) syncDAGs(ctx context.Context) error {
	// Snapshot sources under lock so Reload can update them concurrently.
	s.mu.Lock()
	sources := make([]state.DAGSource, len(s.sources))
	copy(sources, s.sources)
	s.mu.Unlock()

	seen := make(map[string]bool)
	newListeners := make(map[string][]onDAGListener)
	newWebhooks := make(map[string]webhookEntry)

	for _, src := range sources {
		if err := s.syncSource(ctx, src, seen, newListeners, newWebhooks); err != nil {
			s.logger.Error("sync source failed", "source", src.Name, "dir", src.Dir, "error", err)
		}
	}

	// Remove DAGs that no longer exist in any source
	s.mu.Lock()
	for name, entry := range s.registered {
		if !seen[name] {
			s.cron.Remove(entry.cronID)
			entry.teardown()
			delete(s.registered, name)
			s.logger.Info("unregistered DAG", "dag", name)
		}
	}
	// Update on_dag listeners and webhooks
	s.onDAGListeners = newListeners
	s.webhooks = newWebhooks

	// Start/stop webhook server based on whether any webhooks are configured
	needServer := len(newWebhooks) > 0
	hasServer := s.webhookCloseFn != nil
	s.mu.Unlock()

	if needServer && !hasServer {
		addr, closeFn, err := s.startWebhookServer(newWebhooks)
		if err != nil {
			s.logger.Error("failed to start webhook server", "error", err)
		} else {
			s.mu.Lock()
			s.webhookCloseFn = closeFn
			s.webhookAddr = addr
			s.mu.Unlock()
			s.logger.Info("webhook server started", "addr", addr)
		}
	} else if !needServer && hasServer {
		s.mu.Lock()
		closeFn := s.webhookCloseFn
		s.webhookCloseFn = nil
		s.mu.Unlock()
		closeFn()
		s.logger.Info("webhook server stopped (no webhook triggers)")
	}

	return nil
}

// syncSource scans a single DAG source directory.
func (s *Scheduler) syncSource(ctx context.Context, src state.DAGSource, seen map[string]bool, newListeners map[string][]onDAGListener, newWebhooks map[string]webhookEntry) error {
	entries, err := os.ReadDir(src.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read DAG dir %s: %w", src.Dir, err)
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

		// Check and teardown under lock to prevent races with triggerRun
		s.mu.Lock()
		existing, exists := s.registered[d.Name]
		if exists && existing.hash == hash {
			s.mu.Unlock()
			continue // no change
		}
		if exists {
			s.cron.Remove(existing.cronID)
			existing.teardown()
			delete(s.registered, d.Name)
			s.logger.Info("updating DAG triggers", "dag", d.Name)
		} else {
			s.logger.Info("registering DAG", "dag", d.Name)
		}
		s.mu.Unlock()

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

		// Set up condition trigger
		if d.Trigger.Condition != nil {
			cancelFn := s.setupConditionTrigger(ctx, d, dagPath)
			if cancelFn != nil {
				newEntry.cancelFns = append(newEntry.cancelFns, cancelFn)
			}
		}

		// Set up git trigger
		if d.Trigger.Git != nil {
			cancelFn := s.setupGitTrigger(ctx, d, dagPath)
			if cancelFn != nil {
				newEntry.cancelFns = append(newEntry.cancelFns, cancelFn)
			}
		}

		// Register on_dag listener
		if d.Trigger.OnDAG != nil {
			upstream := d.Trigger.OnDAG.Name
			status := d.Trigger.OnDAG.Status
			if status == "" {
				status = "completed"
			}
			newListeners[upstream] = append(newListeners[upstream], onDAGListener{
				dagPath: dagPath,
				status:  status,
			})
		}

		// Register webhook
		if d.Trigger.Webhook != nil {
			newWebhooks[d.Name] = webhookEntry{
				dagPath: dagPath,
				secret:  d.Trigger.Webhook.Secret,
			}
		}

		s.mu.Lock()
		s.registered[d.Name] = newEntry
		s.mu.Unlock()
	}

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

// setupConditionTrigger starts a polling goroutine that evaluates an R expression or
// shell command periodically and triggers a run when it succeeds (exit code 0).
func (s *Scheduler) setupConditionTrigger(ctx context.Context, d *dag.DAG, dagPath string) context.CancelFunc {
	c := d.Trigger.Condition

	pollInterval := 5 * time.Minute
	if c.PollInterval != "" {
		if parsed, err := time.ParseDuration(c.PollInterval); err == nil {
			pollInterval = parsed
		}
	}

	condCtx, cancel := context.WithCancel(ctx)

	s.logger.Info("condition trigger started", "dag", d.Name, "poll_interval", pollInterval)

	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-condCtx.Done():
				return
			case <-ticker.C:
				var cmd *exec.Cmd
				switch {
				case c.Command != "":
					cmd = exec.CommandContext(condCtx, "sh", "-c", c.Command)
				case c.RExpr != "":
					cmd = exec.CommandContext(condCtx, "Rscript", "-e", c.RExpr)
				default:
					continue
				}
				if err := cmd.Run(); err == nil {
					s.logger.Info("condition met, triggering run", "dag", d.Name)
					s.triggerRun(dagPath, "condition")
				}
			}
		}
	}()

	return cancel
}

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
				cmd := exec.CommandContext(gitCtx, "git", "-C", repoDir, "rev-parse", branch)
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

// dispatchCompletions listens for DAG completion events and triggers on_dag listeners.
func (s *Scheduler) dispatchCompletions(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-s.completions:
			s.mu.Lock()
			listeners := s.onDAGListeners[event.DAGName]
			s.mu.Unlock()

			for _, listener := range listeners {
				if listener.status == "any" || listener.status == event.Status || (listener.status == "" && event.Status == "completed") {
					s.logger.Info("on_dag trigger fired", "upstream", event.DAGName, "status", event.Status, "downstream", listener.dagPath)
					s.triggerRun(listener.dagPath, "on_dag")
				}
			}
		}
	}
}

// triggerRun executes a DAG run. Called by any trigger source.
func (s *Scheduler) triggerRun(dagPath string, source string) {
	d, err := dag.ParseFile(dagPath)
	if err != nil {
		s.logger.Error("failed to parse DAG for run", "path", dagPath, "error", err)
		return
	}

	// Determine overlap policy
	overlap := "skip"
	if d.Trigger != nil && d.Trigger.Overlap != "" {
		overlap = d.Trigger.Overlap
	}

	s.mu.Lock()

	// Handle overlap with already-running DAG
	if old, running := s.running[d.Name]; running {
		if overlap == "cancel" {
			s.logger.Info("cancelling previous run (overlap: cancel)", "dag", d.Name, "source", source)
			old.cancel()
			doneCh := old.done
			s.mu.Unlock()
			// Wait for the old run to finish cleanup
			select {
			case <-doneCh:
			case <-time.After(5 * time.Second):
				s.logger.Warn("timeout waiting for cancelled run", "dag", d.Name)
			}
			s.mu.Lock()
		} else {
			s.mu.Unlock()
			s.logger.Info("skipping run, DAG already active", "dag", d.Name, "source", source)
			return
		}
	}

	// Check max concurrent
	if s.runningCount >= s.maxConcurrent {
		s.mu.Unlock()
		s.logger.Warn("skipping run, max concurrent DAGs reached", "dag", d.Name, "max", s.maxConcurrent)
		return
	}

	parentCtx := s.ctx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithCancel(parentCtx)
	doneCh := make(chan struct{})
	s.running[d.Name] = &runEntry{cancel: cancel, done: doneCh}
	s.runningCount++
	s.mu.Unlock()

	// Run in a goroutine
	go func() {
		defer close(doneCh)
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

		status := "completed"
		if err := eng.Run(ctx); err != nil {
			status = "failed"
			s.logger.Error("triggered run failed", "dag", d.Name, "run_id", run.ID, "source", source, "error", err)
		} else {
			s.logger.Info("triggered run completed", "dag", d.Name, "run_id", run.ID, "source", source)
		}

		// Emit completion event for on_dag listeners
		select {
		case s.completions <- dagCompletionEvent{DAGName: d.Name, Status: status}:
		case <-time.After(5 * time.Second):
			s.logger.Error("completion event dropped after timeout", "dag", d.Name)
		}
	}()
}

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
		cmd = exec.CommandContext(ctx, "Rscript", "-e", hook.RExpr)
	case hook.Command != "":
		cmd = exec.CommandContext(ctx, "sh", "-c", hook.Command)
	default:
		return
	}

	if err := cmd.Run(); err != nil {
		s.logger.Error("deadline hook failed", "dag", dagName, "error", err)
	} else {
		s.logger.Info("deadline hook completed", "dag", dagName)
	}
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
