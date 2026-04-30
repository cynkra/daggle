package scheduler

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/cynkra/daggle/cache"
	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/internal/engine"
	"github.com/cynkra/daggle/internal/executor"
	"github.com/cynkra/daggle/state"
)

const (
	defaultPollInterval   = 30 * time.Second
	defaultMaxConcurrent  = 4
	shutdownGracePeriod   = 5 * time.Minute
	defaultDebounce       = 500 * time.Millisecond
	defaultMaxCatchupRuns = 100
)

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
	registered     map[string]*dagEntry // dagName -> entry
	running        map[string]*runEntry // dagName -> active run
	runningCount   int
	maxConcurrent  int
	onDAGListeners map[string][]onDAGListener // upstreamDAGName -> downstream listeners
	completions    chan dagCompletionEvent
	webhooks       map[string]webhookEntry // dagName -> webhook config
	webhookCloseFn func()                  // close the webhook server
	webhookAddr    string                  // address the webhook server is listening on
	ctx            context.Context         // scheduler lifecycle context
	deadlineFired  map[string]string       // dagName -> date string (YYYY-MM-DD) of last fired deadline
	pollInterval   time.Duration
	watchDebounce  time.Duration
	maxCatchupRuns int
	// catchupDone records DAG names for which a catchup pass has already
	// fired in this scheduler process. Initialised once and never cleared,
	// so a DAG file edit (which re-registers the DAG) does not re-fire
	// catchup — only a fresh process start does. Guarded by s.mu.
	catchupDone map[string]bool
	// syncCache short-circuits syncSource when a DAG file's mtime hasn't
	// changed since the last tick. Keyed by absolute file path. Guarded by
	// s.mu along with the rest of the registration state.
	syncCache map[string]syncCacheEntry
	// runtimeSchedules are cron entries created via the REST API (stored in
	// DAGGLE_CONFIG_DIR/schedules.yaml), independent of YAML-declared triggers.
	// Keyed by schedule ID. Guarded by s.mu.
	runtimeSchedules map[string]*runtimeSchedule
}

// syncCacheEntry records the mtime and derived DAG name for a file so a
// subsequent syncSource tick can skip re-hashing and re-parsing when nothing
// has changed on disk. dagName == "" means the file was parseable but had no
// triggers (or parse failed); we still short-circuit so we don't warn on
// every tick.
type syncCacheEntry struct {
	mtime   time.Time
	dagName string
}

// New creates a new Scheduler that watches the given DAG sources.
func New(sources []state.DAGSource) *Scheduler {
	return NewWithConfig(sources, state.SchedulerConfig{})
}

// NewWithConfig creates a new Scheduler with explicit configuration.
func NewWithConfig(sources []state.DAGSource, cfg state.SchedulerConfig) *Scheduler {
	maxConc := cfg.MaxConcurrent
	if maxConc <= 0 {
		maxConc = defaultMaxConcurrent
	}
	pollInt := defaultPollInterval
	if cfg.PollInterval != "" {
		if d, err := time.ParseDuration(cfg.PollInterval); err == nil {
			pollInt = d
		}
	}
	debounce := defaultDebounce
	if cfg.WatchDebounce != "" {
		if d, err := time.ParseDuration(cfg.WatchDebounce); err == nil {
			debounce = d
		}
	}
	maxCatchup := defaultMaxCatchupRuns
	if cfg.MaxCatchupRuns > 0 {
		maxCatchup = cfg.MaxCatchupRuns
	}
	return &Scheduler{
		cron:             cron.New(),
		sources:          sources,
		logger:           slog.Default(),
		registered:       make(map[string]*dagEntry),
		running:          make(map[string]*runEntry),
		maxConcurrent:    maxConc,
		onDAGListeners:   make(map[string][]onDAGListener),
		completions:      make(chan dagCompletionEvent, 256),
		webhooks:         make(map[string]webhookEntry),
		deadlineFired:    make(map[string]string),
		pollInterval:     pollInt,
		watchDebounce:    debounce,
		maxCatchupRuns:   maxCatchup,
		catchupDone:      make(map[string]bool),
		syncCache:        make(map[string]syncCacheEntry),
		runtimeSchedules: make(map[string]*runtimeSchedule),
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
	for _, rs := range s.runtimeSchedules {
		if rs.entry.Enabled {
			triggers["runtime_schedule"]++
		}
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

	// Load runtime schedules from DAGGLE_CONFIG_DIR/schedules.yaml after the
	// DAG scan so dagPathFor can resolve registered DAGs.
	s.loadRuntimeSchedules()

	s.cron.Start()
	s.logger.Info("scheduler started", "sources", len(s.sources))

	// Start on_dag completion dispatcher
	go s.dispatchCompletions(ctx)

	// Start auto-cleanup if configured
	cfg, err := state.LoadConfig()
	if err != nil {
		s.logger.Warn("failed to load config, skipping auto-cleanup", "error", err)
	}
	state.InitTools(cfg)
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
	ticker := time.NewTicker(s.pollInterval)
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

// triggerRun executes a DAG run. Called by any trigger source.
func (s *Scheduler) triggerRun(dagPath string, source string) {
	s.triggerRunWithParams(dagPath, source, nil)
}

// triggerRunWithParams is the params-aware variant used by runtime schedules.
// Passing a nil map is equivalent to triggerRun.
func (s *Scheduler) triggerRunWithParams(dagPath string, source string, params map[string]string) {
	d, err := dag.ParseFile(dagPath)
	if err != nil {
		s.logger.Error("failed to parse DAG for run", "path", dagPath, "error", err)
		s.recordTriggerFailure(state.DAGNameFromFile(dagPath), source, fmt.Errorf("parse DAG: %w", err))
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
			// Wait for the old run to finish cleanup. Use a Timer so the goroutine
			// scheduled by time.After doesn't leak when doneCh fires first.
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-doneCh:
				timer.Stop()
			case <-timer.C:
				s.logger.Warn("timeout waiting for cancelled run", "dag", d.Name)
			}
			s.mu.Lock()
			// While we were waiting without the lock, another trigger may have
			// won the race to register a new run for this DAG. Bail so we don't
			// clobber its cancel/done channels and leak runningCount.
			if _, stillRunning := s.running[d.Name]; stillRunning {
				s.mu.Unlock()
				s.logger.Info("another trigger registered during cancel wait; skipping", "dag", d.Name, "source", source)
				return
			}
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

		expanded, err := dag.ExpandDAG(d, params)
		if err != nil {
			s.logger.Error("template expansion failed", "dag", d.Name, "error", err)
			s.recordTriggerFailure(d.Name, source, fmt.Errorf("template expansion: %w", err))
			return
		}

		// Resolve secret references so ${env:}/${file:}/${vault:} don't reach
		// step processes as literals, and so the redactor has the actual
		// values to mask.
		if err := dag.ResolveEnv(expanded.Env); err != nil {
			s.logger.Error("resolve env failed", "dag", d.Name, "error", err)
			s.recordTriggerFailure(d.Name, source, fmt.Errorf("resolve env: %w", err))
			return
		}
		for i := range expanded.Steps {
			if err := dag.ResolveEnv(expanded.Steps[i].Env); err != nil {
				s.logger.Error("resolve step env failed", "dag", d.Name, "step", expanded.Steps[i].ID, "error", err)
				s.recordTriggerFailure(d.Name, source, fmt.Errorf("resolve env for step %q: %w", expanded.Steps[i].ID, err))
				return
			}
		}

		run, err := state.CreateRun(expanded.Name)
		if err != nil {
			s.logger.Error("create run failed", "dag", d.Name, "error", err)
			return
		}

		cacheDir := filepath.Join(state.DataDir(), "cache")
		engCfg := engine.Config{
			DAG:         expanded,
			Run:         run,
			ExecFactory: executor.New,
			Redactor:    dag.NewRedactor(dag.AllEnvMaps(expanded)...),
			CacheStore:  cache.NewStore(cacheDir),
		}
		if cfg, err := state.LoadConfig(); err == nil && cfg.Notifications != nil {
			engCfg.Notifications = cfg.Notifications
		}
		eng, err := engine.New(engCfg)
		if err != nil {
			s.logger.Error("engine init failed", "dag", d.Name, "error", err)
			// Run dir already exists from CreateRun above; record the
			// failure into it so the UI shows "failed" instead of "unknown".
			writeRunFailureEvents(run.Dir, fmt.Errorf("engine init: %w", err))
			return
		}

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

// recordTriggerFailure persists a trigger attempt that failed before the
// engine could take over (parse error, template expansion, env resolution).
// Without this, the UI would show "never run" for DAGs whose triggers are
// failing because no run directory ever exists on disk.
//
// Best effort: an invalid DAG name or filesystem error is logged and dropped
// rather than panicking the trigger source.
func (s *Scheduler) recordTriggerFailure(dagName, source string, cause error) {
	if state.ValidateDAGName(dagName) != nil {
		return
	}
	run, err := state.CreateRun(dagName)
	if err != nil {
		s.logger.Error("could not record trigger failure", "dag", dagName, "source", source, "error", err)
		return
	}
	writeRunFailureEvents(run.Dir, cause)
}

// writeRunFailureEvents writes a Started+Failed event pair to an existing run
// directory. Used by the scheduler to surface failures that happen between
// run-dir creation and engine handoff.
func writeRunFailureEvents(runDir string, cause error) {
	w := state.NewEventWriter(runDir)
	defer func() { _ = w.Close() }()
	_ = w.Write(state.Event{Type: state.EventRunStarted})
	_ = w.Write(state.Event{Type: state.EventRunFailed, Error: cause.Error()})
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
