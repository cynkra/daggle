package scheduler

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/rs/xid"

	"github.com/cynkra/daggle/state"
)

// ScheduleView is a read-only snapshot of a schedule (YAML-declared or
// runtime) suitable for the API layer. NextRun is the zero value when the
// schedule is disabled or the cron library has not yet computed a next fire.
type ScheduleView struct {
	ID      string
	Cron    string
	Source  string // "yaml" or "runtime"
	Enabled bool
	NextRun time.Time
	Params  map[string]string
}

// runtimeSchedule pairs a stored schedule with its live cron entry ID. A zero
// cronID means the schedule is disabled and not currently registered with the
// cron scheduler.
type runtimeSchedule struct {
	dagName string
	entry   state.ScheduleEntry
	dagPath string
	cronID  cron.EntryID
}

// loadRuntimeSchedules registers every enabled runtime schedule in schedules.yaml
// with the cron scheduler. Called once at Start; silently skips schedules
// whose DAG is not currently registered (they'll be picked up when the DAG
// comes online).
func (s *Scheduler) loadRuntimeSchedules() {
	all, err := state.LoadSchedules()
	if err != nil {
		s.logger.Warn("failed to load runtime schedules", "error", err)
		return
	}
	if s.runtimeSchedules == nil {
		s.runtimeSchedules = make(map[string]*runtimeSchedule)
	}
	for dagName, entries := range all {
		dagPath := s.dagPathFor(dagName)
		if dagPath == "" {
			s.logger.Warn("runtime schedules for unregistered DAG; skipping", "dag", dagName, "count", len(entries))
			continue
		}
		for _, e := range entries {
			rs := &runtimeSchedule{dagName: dagName, entry: e, dagPath: dagPath}
			if e.Enabled {
				id, err := s.cron.AddFunc(e.Cron, func() {
					s.triggerRunWithParams(dagPath, "runtime-schedule", e.Params)
				})
				if err != nil {
					s.logger.Error("invalid runtime cron; leaving disabled", "dag", dagName, "id", e.ID, "cron", e.Cron, "error", err)
				} else {
					rs.cronID = id
				}
			}
			s.runtimeSchedules[e.ID] = rs
		}
	}
}

// dagPathFor looks up the file path of a registered DAG. Returns "" if the
// DAG is not registered yet (e.g. schedule loaded before the DAG file was
// discovered).
func (s *Scheduler) dagPathFor(dagName string) string {
	sources := make([]state.DAGSource, len(s.sources))
	s.mu.Lock()
	copy(sources, s.sources)
	s.mu.Unlock()
	for _, src := range sources {
		for _, ext := range []string{".yaml", ".yml"} {
			candidate := fmt.Sprintf("%s/%s%s", src.Dir, dagName, ext)
			if fileExists(candidate) {
				return candidate
			}
		}
	}
	return ""
}

// ListSchedules returns every YAML-declared and runtime schedule for a DAG.
// Safe to call even if no scheduler loop is running — NextRun is still
// populated via a direct cron.ParseStandard(cronExpr).Next(time.Now()) when
// the cron entry's cached Next is zero (e.g. before cron.Start).
func (s *Scheduler) ListSchedules(dagName string) []ScheduleView {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []ScheduleView

	// YAML-declared (at most one per DAG today).
	if e, ok := s.registered[dagName]; ok && e.schedule != "" {
		out = append(out, ScheduleView{
			ID:      "yaml-" + dagName,
			Cron:    e.schedule,
			Source:  "yaml",
			Enabled: true,
			NextRun: s.nextRun(e.cronID, e.schedule),
		})
	}

	// Runtime.
	for _, rs := range s.runtimeSchedules {
		if rs.dagName != dagName {
			continue
		}
		view := ScheduleView{
			ID:      rs.entry.ID,
			Cron:    rs.entry.Cron,
			Source:  "runtime",
			Enabled: rs.entry.Enabled,
			Params:  rs.entry.Params,
		}
		if rs.entry.Enabled {
			view.NextRun = s.nextRun(rs.cronID, rs.entry.Cron)
		}
		out = append(out, view)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// nextRun reads the cached Next time from the cron entry, falling back to
// parsing the expression if the cron scheduler hasn't started yet (Entry.Next
// is a zero value until the cron loop ticks). Must be called with s.mu held.
func (s *Scheduler) nextRun(id cron.EntryID, expr string) time.Time {
	if id == 0 {
		return time.Time{}
	}
	if next := s.cron.Entry(id).Next; !next.IsZero() {
		return next
	}
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return time.Time{}
	}
	return sched.Next(time.Now())
}

// AddRuntimeSchedule validates the cron expression, persists the entry, and
// registers it with the cron scheduler (if enabled). Returns the resulting
// view — includes the generated ID and the first NextRun.
func (s *Scheduler) AddRuntimeSchedule(dagName string, cronExpr string, params map[string]string, enabled bool) (ScheduleView, error) {
	dagPath := s.dagPathFor(dagName)
	if dagPath == "" {
		return ScheduleView{}, fmt.Errorf("DAG %q is not registered", dagName)
	}
	if _, err := cron.ParseStandard(cronExpr); err != nil {
		return ScheduleView{}, fmt.Errorf("invalid cron expression: %w", err)
	}

	entry := state.ScheduleEntry{
		ID:      "sch_" + xid.New().String(),
		Cron:    cronExpr,
		Enabled: enabled,
		Params:  params,
	}
	if err := state.AddSchedule(dagName, entry); err != nil {
		return ScheduleView{}, fmt.Errorf("persist: %w", err)
	}

	rs := &runtimeSchedule{dagName: dagName, entry: entry, dagPath: dagPath}
	if enabled {
		id, err := s.cron.AddFunc(cronExpr, func() {
			s.triggerRunWithParams(dagPath, "runtime-schedule", params)
		})
		if err != nil {
			// Shouldn't happen because we validated above, but roll back
			// storage to keep state consistent.
			_ = state.RemoveSchedule(dagName, entry.ID)
			return ScheduleView{}, fmt.Errorf("register cron: %w", err)
		}
		rs.cronID = id
	}

	s.mu.Lock()
	if s.runtimeSchedules == nil {
		s.runtimeSchedules = make(map[string]*runtimeSchedule)
	}
	s.runtimeSchedules[entry.ID] = rs
	var next time.Time
	if entry.Enabled {
		next = s.nextRun(rs.cronID, entry.Cron)
	}
	s.mu.Unlock()

	return ScheduleView{
		ID:      entry.ID,
		Cron:    entry.Cron,
		Source:  "runtime",
		Enabled: entry.Enabled,
		NextRun: next,
		Params:  entry.Params,
	}, nil
}

// RemoveRuntimeSchedule unregisters from cron and deletes from storage. It is
// NOT allowed on YAML-declared schedules — callers should catch ErrYAMLSchedule
// and return 400 to the user.
func (s *Scheduler) RemoveRuntimeSchedule(dagName, scheduleID string) error {
	if isYAMLScheduleID(scheduleID) {
		return ErrYAMLSchedule
	}

	s.mu.Lock()
	rs, ok := s.runtimeSchedules[scheduleID]
	if ok && rs.dagName == dagName {
		if rs.cronID != 0 {
			s.cron.Remove(rs.cronID)
		}
		delete(s.runtimeSchedules, scheduleID)
	}
	s.mu.Unlock()

	if err := state.RemoveSchedule(dagName, scheduleID); err != nil {
		return err
	}
	return nil
}

// SetRuntimeScheduleEnabled toggles enabled and registers/unregisters with cron
// accordingly. Idempotent when the flag already matches the stored value.
func (s *Scheduler) SetRuntimeScheduleEnabled(dagName, scheduleID string, enabled bool) (ScheduleView, error) {
	if isYAMLScheduleID(scheduleID) {
		return ScheduleView{}, ErrYAMLSchedule
	}

	updated, err := state.UpdateScheduleEnabled(dagName, scheduleID, enabled)
	if err != nil {
		return ScheduleView{}, err
	}

	s.mu.Lock()
	rs, ok := s.runtimeSchedules[scheduleID]
	if !ok {
		// Schedule exists in storage but not in the in-memory map — rebuild
		// the in-memory entry so the next-run is computable.
		dagPath := s.dagPathFor(dagName)
		s.mu.Unlock()
		if dagPath == "" {
			return ScheduleView{ID: updated.ID, Cron: updated.Cron, Source: "runtime", Enabled: updated.Enabled, Params: updated.Params}, nil
		}
		s.mu.Lock()
		rs = &runtimeSchedule{dagName: dagName, entry: updated, dagPath: dagPath}
		s.runtimeSchedules[scheduleID] = rs
	}

	// Apply the enabled transition.
	if enabled && rs.cronID == 0 {
		id, err := s.cron.AddFunc(updated.Cron, func() {
			s.triggerRunWithParams(rs.dagPath, "runtime-schedule", updated.Params)
		})
		if err != nil {
			s.mu.Unlock()
			return ScheduleView{}, fmt.Errorf("register cron: %w", err)
		}
		rs.cronID = id
	} else if !enabled && rs.cronID != 0 {
		s.cron.Remove(rs.cronID)
		rs.cronID = 0
	}
	rs.entry = updated
	var next time.Time
	if updated.Enabled {
		next = s.nextRun(rs.cronID, updated.Cron)
	}
	s.mu.Unlock()

	return ScheduleView{
		ID:      updated.ID,
		Cron:    updated.Cron,
		Source:  "runtime",
		Enabled: updated.Enabled,
		NextRun: next,
		Params:  updated.Params,
	}, nil
}

// ErrYAMLSchedule is returned when a caller tries to mutate a YAML-declared
// schedule via the runtime API. YAML triggers are owned by the DAG file and
// can only be changed by editing it.
var ErrYAMLSchedule = fmt.Errorf("cannot modify yaml-declared schedule; edit the DAG file")

// isYAMLScheduleID returns true for the synthetic IDs we assign to YAML-
// declared triggers (format: "yaml-<dag-name>").
func isYAMLScheduleID(id string) bool {
	return len(id) > 5 && id[:5] == "yaml-"
}

// fileExists reports whether the path names an existing regular file.
func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
