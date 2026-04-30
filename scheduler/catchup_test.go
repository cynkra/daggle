package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/cynkra/daggle/state"
)

func mustCron(t *testing.T, expr string) cron.Schedule {
	t.Helper()
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		t.Fatalf("parse %q: %v", expr, err)
	}
	return sched
}

func TestMissedTicks(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		lastEnd      time.Time
		now          time.Time
		expr         string
		mode         string
		cap          int
		wantLen      int
		wantTrunc    bool
		wantLastTick time.Time
	}{
		{
			name:         "once with three missed hourly ticks fires only the most recent",
			lastEnd:      now.Add(-3 * time.Hour).Add(-30 * time.Minute), // 08:30
			now:          now,
			expr:         "0 * * * *",
			mode:         "once",
			cap:          100,
			wantLen:      1,
			wantLastTick: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
		},
		{
			// lastEnd 08:30; ticks at 09, 10, 11, 12 are <= now (12:00).
			// All four count as missed.
			name:    "all with four missed hourly ticks fires four runs",
			lastEnd: now.Add(-3 * time.Hour).Add(-30 * time.Minute),
			now:     now,
			expr:    "0 * * * *",
			mode:    "all",
			cap:     100,
			wantLen: 4,
		},
		{
			name:    "empty mode catches up nothing",
			lastEnd: now.Add(-3 * time.Hour),
			now:     now,
			expr:    "0 * * * *",
			mode:    "",
			cap:     100,
			wantLen: 0,
		},
		{
			name:    "off mode catches up nothing",
			lastEnd: now.Add(-3 * time.Hour),
			now:     now,
			expr:    "0 * * * *",
			mode:    "off",
			cap:     100,
			wantLen: 0,
		},
		{
			// lastEnd 12:15, now 12:30, hourly schedule -> next tick is 13:00,
			// which is after now, so nothing to catch up.
			name:    "no missed tick yet returns nothing",
			lastEnd: now.Add(15 * time.Minute),
			now:     now.Add(30 * time.Minute),
			expr:    "0 * * * *",
			mode:    "all",
			cap:     100,
			wantLen: 0,
		},
		{
			name:      "all with cap truncates and reports it",
			lastEnd:   now.Add(-7 * 24 * time.Hour),
			now:       now,
			expr:      "* * * * *",
			mode:      "all",
			cap:       100,
			wantLen:   100,
			wantTrunc: true,
		},
		{
			name:    "zero lastEnd disables catchup",
			lastEnd: time.Time{},
			now:     now,
			expr:    "0 * * * *",
			mode:    "once",
			cap:     100,
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ticks, trunc := missedTicks(mustCron(t, tt.expr), tt.lastEnd, tt.now, tt.mode, tt.cap)
			if len(ticks) != tt.wantLen {
				t.Errorf("len(ticks)=%d want %d (ticks=%v)", len(ticks), tt.wantLen, ticks)
			}
			if trunc != tt.wantTrunc {
				t.Errorf("truncated=%v want %v", trunc, tt.wantTrunc)
			}
			if !tt.wantLastTick.IsZero() && len(ticks) > 0 {
				if !ticks[len(ticks)-1].Equal(tt.wantLastTick) {
					t.Errorf("last tick = %v, want %v", ticks[len(ticks)-1], tt.wantLastTick)
				}
			}
			// All ticks must be strictly after lastEnd and not after now.
			for _, ti := range ticks {
				if !ti.After(tt.lastEnd) {
					t.Errorf("tick %v is not after lastEnd %v", ti, tt.lastEnd)
				}
				if ti.After(tt.now) {
					t.Errorf("tick %v is after now %v", ti, tt.now)
				}
			}
			// "all" ticks should be in ascending order.
			if tt.mode == "all" {
				for i := 1; i < len(ticks); i++ {
					if !ticks[i].After(ticks[i-1]) {
						t.Errorf("ticks not ascending at %d: %v then %v", i, ticks[i-1], ticks[i])
					}
				}
			}
		})
	}
}

func TestMissedTicks_NilSchedule(t *testing.T) {
	ticks, trunc := missedTicks(nil, time.Now().Add(-time.Hour), time.Now(), "all", 100)
	if len(ticks) != 0 || trunc {
		t.Errorf("nil schedule: got ticks=%v trunc=%v, want empty", ticks, trunc)
	}
}

// seedTerminalRun creates a fake completed-run on disk for dagName whose
// last event has the given Timestamp. Used to anchor the catchup
// last-end-time calculation back into the past.
func seedTerminalRun(t *testing.T, dagName string, endTime time.Time) {
	t.Helper()
	run, err := state.CreateRun(dagName)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	w := state.NewEventWriter(run.Dir)
	defer func() { _ = w.Close() }()
	if err := w.Write(state.Event{Type: state.EventRunStarted, Timestamp: endTime.Add(-time.Second)}); err != nil {
		t.Fatalf("write started: %v", err)
	}
	if err := w.Write(state.Event{Type: state.EventRunCompleted, Timestamp: endTime}); err != nil {
		t.Fatalf("write completed: %v", err)
	}
}

// waitFor polls cond every 20ms up to timeout. Returns true on success.
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

func TestScheduler_CatchupOnceFiresOneRun(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0o755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	// Pre-seed a completed run that ended 2h ago.
	seedTerminalRun(t, "catchup-once", time.Now().Add(-2*time.Hour))

	writeDAG(t, dagDir, "catchup-once.yaml", `
name: catchup-once
trigger:
  schedule: "@every 30m"
  catchup: once
steps:
  - id: slow
    command: sleep 30
`)

	sched := New([]state.DAGSource{{Name: "test", Dir: dagDir}})
	if err := sched.syncDAGs(context.Background()); err != nil {
		t.Fatalf("syncDAGs: %v", err)
	}

	if !waitFor(2*time.Second, func() bool {
		sched.mu.Lock()
		defer sched.mu.Unlock()
		return len(sched.running) == 1
	}) {
		sched.mu.Lock()
		got := len(sched.running)
		sched.mu.Unlock()
		t.Fatalf("expected one catchup run to fire, got running=%d", got)
	}

	// Re-running syncDAGs MUST NOT fire catchup again.
	if err := sched.syncDAGs(context.Background()); err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	sched.mu.Lock()
	running := len(sched.running)
	sched.mu.Unlock()
	if running != 1 {
		t.Errorf("after re-sync: running = %d, want 1 (catchup must not re-fire)", running)
	}

	// Cleanup
	sched.mu.Lock()
	for _, e := range sched.running {
		e.cancel()
	}
	sched.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
}

func TestScheduler_CatchupNoHistorySkips(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0o755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	// No pre-seeded run.
	writeDAG(t, dagDir, "fresh.yaml", `
name: catchup-fresh
trigger:
  schedule: "@every 30m"
  catchup: once
steps:
  - id: slow
    command: sleep 30
`)

	sched := New([]state.DAGSource{{Name: "test", Dir: dagDir}})
	if err := sched.syncDAGs(context.Background()); err != nil {
		t.Fatalf("syncDAGs: %v", err)
	}

	// Give the runCatchup goroutine a chance to evaluate and bail.
	time.Sleep(300 * time.Millisecond)

	sched.mu.Lock()
	running := len(sched.running)
	sched.mu.Unlock()
	if running != 0 {
		t.Errorf("running = %d, want 0 (no history → no catchup)", running)
	}
}

func TestScheduler_CatchupAllCappedRunsSerially(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0o755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	// Last terminal run ended 24h ago — many missed ticks.
	seedTerminalRun(t, "catchup-all", time.Now().Add(-24*time.Hour))

	// Fast-finishing step so the serial wait between catchup fires
	// completes quickly.
	writeDAG(t, dagDir, "catchup-all.yaml", `
name: catchup-all
trigger:
  schedule: "@every 30m"
  catchup: all
steps:
  - id: quick
    command: "true"
`)

	sched := NewWithConfig(
		[]state.DAGSource{{Name: "test", Dir: dagDir}},
		state.SchedulerConfig{MaxCatchupRuns: 2},
	)
	if err := sched.syncDAGs(context.Background()); err != nil {
		t.Fatalf("syncDAGs: %v", err)
	}

	// Two runs should have been created in total. They run serially via
	// waitForRunClear. Wait for them to all finish (each is "true", so fast).
	if !waitFor(5*time.Second, func() bool {
		runs, err := state.ListRuns("catchup-all")
		if err != nil {
			return false
		}
		// One pre-seeded + two catchup = 3 runs.
		return len(runs) >= 3
	}) {
		runs, _ := state.ListRuns("catchup-all")
		t.Fatalf("expected 3 runs (1 seeded + 2 catchup), got %d", len(runs))
	}

	// No more than the cap should fire — even after waiting a bit longer.
	time.Sleep(300 * time.Millisecond)
	runs, _ := state.ListRuns("catchup-all")
	if len(runs) != 3 {
		t.Errorf("len(runs) = %d, want exactly 3 (1 seeded + cap=2 catchup)", len(runs))
	}
}

func TestScheduler_CatchupNotReFiredOnFileEdit(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0o755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	seedTerminalRun(t, "catchup-edit", time.Now().Add(-2*time.Hour))

	writeDAG(t, dagDir, "catchup-edit.yaml", `
name: catchup-edit
trigger:
  schedule: "@every 30m"
  catchup: once
steps:
  - id: slow
    command: sleep 30
`)

	sched := New([]state.DAGSource{{Name: "test", Dir: dagDir}})
	if err := sched.syncDAGs(context.Background()); err != nil {
		t.Fatalf("first syncDAGs: %v", err)
	}

	if !waitFor(2*time.Second, func() bool {
		sched.mu.Lock()
		defer sched.mu.Unlock()
		return len(sched.running) == 1
	}) {
		t.Fatal("initial catchup did not fire")
	}

	// Cancel the catchup run so we can detect a fresh one cleanly.
	sched.mu.Lock()
	for _, e := range sched.running {
		e.cancel()
	}
	sched.mu.Unlock()
	if !waitFor(2*time.Second, func() bool {
		sched.mu.Lock()
		defer sched.mu.Unlock()
		return len(sched.running) == 0
	}) {
		t.Fatal("catchup run did not exit after cancel")
	}

	// Edit the DAG (different command) — forces re-registration.
	writeDAG(t, dagDir, "catchup-edit.yaml", `
name: catchup-edit
trigger:
  schedule: "@every 30m"
  catchup: once
steps:
  - id: slow
    command: sleep 31
`)
	if err := sched.syncDAGs(context.Background()); err != nil {
		t.Fatalf("second syncDAGs: %v", err)
	}

	// Catchup must NOT re-fire — the DAG was running this whole time.
	time.Sleep(300 * time.Millisecond)
	sched.mu.Lock()
	running := len(sched.running)
	sched.mu.Unlock()
	if running != 0 {
		t.Errorf("running = %d, want 0 (catchup must not re-fire on file edit)", running)
	}
}
