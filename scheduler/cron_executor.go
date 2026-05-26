package scheduler

import (
	"time"

	"github.com/robfig/cron/v3"
)

// cronEntry is daggle's own scheduling record. It replaces the
// robfig/cron/v3 internal timer-based execution path: we keep that library
// only for parsing cron expressions (via cron.ParseStandard) and computing
// successive Next times. Execution is driven by the scheduler's poll loop
// calling fireDueCronEntries, which is robust against any timer drift or
// suspend/resume behaviour of the host.
type cronEntry struct {
	schedule cron.Schedule
	expr     string            // original cron expression (for Status/inspection)
	dagPath  string            // path to the DAG YAML, used by triggerRun*
	params   map[string]string // optional params for runtime schedules (nil for YAML triggers)
	next     time.Time         // wall-clock time of the next fire
}

// newCronEntry parses expr, anchors next at the first scheduled fire after
// now, and returns the entry. Returns an error when expr is invalid; callers
// should propagate that to the user.
func newCronEntry(expr, dagPath string, params map[string]string, now time.Time) (*cronEntry, error) {
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return nil, err
	}
	return &cronEntry{
		schedule: sched,
		expr:     expr,
		dagPath:  dagPath,
		params:   params,
		next:     sched.Next(now),
	}, nil
}

// fireDueCronEntries walks every registered cron entry (both YAML-declared
// triggers attached to dagEntry and runtime-API schedules in runtimeSchedules)
// and fires any whose next time has passed. After each fire, next is advanced
// via schedule.Next so a single call can catch up multiple missed ticks. The
// triggerRun* calls happen *after* the snapshot is taken and the lock is
// released, since triggerRun acquires s.mu itself.
func (s *Scheduler) fireDueCronEntries(now time.Time) {
	type pendingFire struct {
		dagPath string
		params  map[string]string
		source  string
	}

	s.mu.Lock()
	var fires []pendingFire

	for _, entry := range s.registered {
		ce := entry.cron
		if ce == nil {
			continue
		}
		for !ce.next.After(now) {
			fires = append(fires, pendingFire{dagPath: ce.dagPath, source: "cron"})
			ce.next = ce.schedule.Next(ce.next)
		}
	}

	for _, rs := range s.runtimeSchedules {
		if !rs.entry.Enabled || rs.cron == nil {
			continue
		}
		for !rs.cron.next.After(now) {
			fires = append(fires, pendingFire{dagPath: rs.cron.dagPath, params: rs.cron.params, source: "runtime-schedule"})
			rs.cron.next = rs.cron.schedule.Next(rs.cron.next)
		}
	}
	s.mu.Unlock()

	for _, f := range fires {
		if f.params == nil {
			s.triggerRun(f.dagPath, f.source)
		} else {
			s.triggerRunWithParams(f.dagPath, f.source, f.params)
		}
	}
}
