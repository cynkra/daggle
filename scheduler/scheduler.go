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

	"github.com/robfig/cron/v3"
	"github.com/schochastics/rdag/dag"
	"github.com/schochastics/rdag/engine"
	"github.com/schochastics/rdag/executor"
	"github.com/schochastics/rdag/state"
)

const (
	defaultPollInterval  = 30 * time.Second
	defaultMaxConcurrent = 4
	shutdownGracePeriod  = 5 * time.Minute
)

// dagEntry tracks a registered DAG and its cron entry.
type dagEntry struct {
	entryID  cron.EntryID
	schedule string
	hash     string // file content hash for change detection
}

// Scheduler manages cron-based DAG execution.
type Scheduler struct {
	cron    *cron.Cron
	dagDir  string
	logger  *slog.Logger

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
	if err := s.syncDAGs(); err != nil {
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
			if err := s.syncDAGs(); err != nil {
				s.logger.Error("DAG sync failed", "error", err)
			}
		}
	}
}

// shutdown performs graceful shutdown.
func (s *Scheduler) shutdown() {
	// Stop cron (no new triggers)
	cronCtx := s.cron.Stop()

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

// syncDAGs scans the DAG directory and updates cron registrations.
func (s *Scheduler) syncDAGs() error {
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

		if d.Schedule == "" {
			continue
		}

		seen[d.Name] = true

		s.mu.Lock()
		existing, exists := s.registered[d.Name]
		s.mu.Unlock()

		if exists && existing.hash == hash {
			continue // no change
		}

		// Remove old entry if schedule changed
		if exists {
			s.cron.Remove(existing.entryID)
			s.logger.Info("updated DAG schedule", "dag", d.Name, "schedule", d.Schedule)
		} else {
			s.logger.Info("registered DAG", "dag", d.Name, "schedule", d.Schedule)
		}

		// Register new cron entry
		dagPath := path // capture for closure
		entryID, err := s.cron.AddFunc(d.Schedule, func() {
			s.triggerRun(dagPath)
		})
		if err != nil {
			s.logger.Error("invalid cron schedule", "dag", d.Name, "schedule", d.Schedule, "error", err)
			continue
		}

		s.mu.Lock()
		s.registered[d.Name] = &dagEntry{
			entryID:  entryID,
			schedule: d.Schedule,
			hash:     hash,
		}
		s.mu.Unlock()
	}

	// Remove DAGs that no longer exist
	s.mu.Lock()
	for name, entry := range s.registered {
		if !seen[name] {
			s.cron.Remove(entry.entryID)
			delete(s.registered, name)
			s.logger.Info("unregistered DAG", "dag", name)
		}
	}
	s.mu.Unlock()

	return nil
}

// triggerRun executes a DAG run. Called by cron.
func (s *Scheduler) triggerRun(dagPath string) {
	d, err := dag.ParseFile(dagPath)
	if err != nil {
		s.logger.Error("failed to parse DAG for run", "path", dagPath, "error", err)
		return
	}

	s.mu.Lock()

	// Skip if already running (overlap policy: skip)
	if _, running := s.running[d.Name]; running {
		s.mu.Unlock()
		s.logger.Info("skipping run, DAG already active", "dag", d.Name)
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

		s.logger.Info("scheduled run starting", "dag", d.Name)

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

		eng := engine.New(expanded, run, func(step dag.Step) executor.Executor {
			return executor.New(step)
		})

		if err := eng.Run(ctx); err != nil {
			s.logger.Error("scheduled run failed", "dag", d.Name, "run_id", run.ID, "error", err)
		} else {
			s.logger.Info("scheduled run completed", "dag", d.Name, "run_id", run.ID)
		}
	}()
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
