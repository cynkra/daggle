package scheduler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
)

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

	s.pruneUnregisteredDAGs(seen, newListeners, newWebhooks)
	s.syncWebhookServer(newWebhooks)
	return nil
}

// pruneUnregisteredDAGs removes registered DAGs that are no longer present in
// any source, drops syncCache entries for deleted files, and swaps in the
// fresh on_dag listener and webhook maps built during the scan. Holds s.mu
// for the whole update.
func (s *Scheduler) pruneUnregisteredDAGs(seen map[string]bool, newListeners map[string][]onDAGListener, newWebhooks map[string]webhookEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, entry := range s.registered {
		if !seen[name] {
			s.cron.Remove(entry.cronID)
			entry.teardown()
			delete(s.registered, name)
			s.logger.Info("unregistered DAG", "dag", name)
		}
	}
	for path := range s.syncCache {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			delete(s.syncCache, path)
		}
	}
	s.onDAGListeners = newListeners
	s.webhooks = newWebhooks
}

// syncWebhookServer starts the webhook HTTP server when webhooks are newly
// configured, or stops it when none remain. Releases s.mu across the
// start/stop call so server I/O doesn't block other scheduler state.
func (s *Scheduler) syncWebhookServer(newWebhooks map[string]webhookEntry) {
	s.mu.Lock()
	needServer := len(newWebhooks) > 0
	hasServer := s.webhookCloseFn != nil
	s.mu.Unlock()

	if needServer && !hasServer {
		addr, closeFn, err := s.startWebhookServer(newWebhooks)
		if err != nil {
			s.logger.Error("failed to start webhook server", "error", err)
			return
		}
		s.mu.Lock()
		s.webhookCloseFn = closeFn
		s.webhookAddr = addr
		s.mu.Unlock()
		s.logger.Info("webhook server started", "addr", addr)
		return
	}
	if !needServer && hasServer {
		s.mu.Lock()
		closeFn := s.webhookCloseFn
		s.webhookCloseFn = nil
		s.mu.Unlock()
		closeFn()
		s.logger.Info("webhook server stopped (no webhook triggers)")
	}
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

		// Short-circuit via mtime cache: if the file's modification time
		// hasn't changed since the last tick, skip hashing and parsing
		// entirely. Keeps idle schedulers cheap even with hundreds of DAGs.
		info, statErr := os.Stat(path)
		if statErr == nil {
			s.mu.Lock()
			if cached, ok := s.syncCache[path]; ok && cached.mtime.Equal(info.ModTime()) {
				if cached.dagName != "" {
					seen[cached.dagName] = true
				}
				s.mu.Unlock()
				continue
			}
			s.mu.Unlock()
		}

		hash, err := fileHash(path)
		if err != nil {
			s.logger.Warn("failed to hash DAG file", "path", path, "error", err)
			continue
		}

		d, err := dag.ParseFile(path)
		if err != nil {
			s.logger.Warn("failed to parse DAG", "path", path, "error", err)
			if info != nil {
				s.mu.Lock()
				s.syncCache[path] = syncCacheEntry{mtime: info.ModTime()}
				s.mu.Unlock()
			}
			continue
		}

		if !d.HasTrigger() {
			// Remember the mtime so we don't re-parse a trigger-less DAG
			// on every tick.
			if info != nil {
				s.mu.Lock()
				s.syncCache[path] = syncCacheEntry{mtime: info.ModTime(), dagName: d.Name}
				s.mu.Unlock()
			}
			continue
		}

		seen[d.Name] = true
		if info != nil {
			s.mu.Lock()
			s.syncCache[path] = syncCacheEntry{mtime: info.ModTime(), dagName: d.Name}
			s.mu.Unlock()
		}

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
				if mode := d.Trigger.Catchup; mode == "once" || mode == "all" {
					if s.catchupOnce(d.Name) {
						go s.runCatchup(d.Name, dagPath, sched, mode)
					}
				}
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
