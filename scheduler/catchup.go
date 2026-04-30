package scheduler

import (
	"context"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/cynkra/daggle/state"
)

// catchupOnce returns true the first time it is called for dagName in this
// scheduler process and false thereafter. The guard ensures catchup fires on
// a cold scheduler start but does NOT re-fire when a DAG file edit causes
// re-registration via syncSource.
func (s *Scheduler) catchupOnce(dagName string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.catchupDone[dagName] {
		return false
	}
	s.catchupDone[dagName] = true
	return true
}

// missedTicks returns the cron tick times in (lastEnd, now] that should be
// caught up given the policy mode.
//
//   - mode == "once": at most one tick (the most recent missed tick).
//   - mode == "all":  every missed tick, capped at cap. truncated=true when
//     additional ticks would have been returned without the cap.
//   - any other:      no ticks.
//
// A zero lastEnd or a nil sched returns no ticks (no anchor / no schedule).
func missedTicks(sched cron.Schedule, lastEnd, now time.Time, mode string, cap int) (ticks []time.Time, truncated bool) {
	if sched == nil || lastEnd.IsZero() {
		return nil, false
	}
	if mode != "once" && mode != "all" {
		return nil, false
	}

	var all []time.Time
	t := sched.Next(lastEnd)
	for !t.After(now) {
		all = append(all, t)
		if mode == "all" && cap > 0 && len(all) > cap {
			truncated = true
			all = all[:cap]
			break
		}
		t = sched.Next(t)
	}
	if len(all) == 0 {
		return nil, false
	}
	if mode == "once" {
		return all[len(all)-1:], false
	}
	return all, truncated
}

// runCatchup is invoked in its own goroutine at most once per (DAG, process).
// It resolves the last terminal run, computes the missed ticks, and fires
// triggerRun calls with source="catchup". For mode=="all", runs are
// serialised: each subsequent fire waits for the previous to clear from
// s.running so the overlap-skip policy does not drop them.
func (s *Scheduler) runCatchup(dagName, dagPath, schedule, mode string) {
	parsed, err := cron.ParseStandard(schedule)
	if err != nil {
		s.logger.Warn("catchup skipped: invalid schedule", "dag", dagName, "schedule", schedule, "error", err)
		return
	}

	lastEnd, ok, err := state.LastRunEndTime(dagName)
	if err != nil {
		s.logger.Warn("catchup skipped: cannot read run history", "dag", dagName, "error", err)
		return
	}
	if !ok {
		s.logger.Info("catchup skipped: no prior run history", "dag", dagName)
		return
	}

	ticks, truncated := missedTicks(parsed, lastEnd, time.Now(), mode, s.maxCatchupRuns)
	if len(ticks) == 0 {
		return
	}
	if truncated {
		s.logger.Warn("catchup truncated", "dag", dagName, "fired", len(ticks), "cap", s.maxCatchupRuns)
	}
	s.logger.Info("catchup starting", "dag", dagName, "missed_ticks", len(ticks), "mode", mode, "last_end", lastEnd)

	ctx := s.ctx
	for i, tick := range ticks {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
		s.logger.Info("catchup firing", "dag", dagName, "tick", tick, "n", i+1, "of", len(ticks))
		s.triggerRun(dagPath, "catchup")

		if mode != "all" || i == len(ticks)-1 {
			continue
		}
		// Wait for the just-fired catchup run to clear before firing the
		// next tick. Without this, overlap=skip would drop subsequent fires.
		s.waitForRunClear(ctx, dagName)
	}
}

// waitForRunClear blocks until s.running no longer contains dagName, or until
// ctx is cancelled. Used by runCatchup to serialise catchup: all firings.
func (s *Scheduler) waitForRunClear(ctx context.Context, dagName string) {
	var done <-chan struct{}
	if ctx != nil {
		done = ctx.Done()
	}
	for {
		s.mu.Lock()
		_, busy := s.running[dagName]
		s.mu.Unlock()
		if !busy {
			return
		}
		select {
		case <-done:
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}
