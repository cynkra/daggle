//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cynkra/daggle/scheduler"
	"github.com/cynkra/daggle/state"
)

// TestSchedulerFiresDAG registers a DAG with an @every 1s schedule, starts
// the scheduler, and waits for a run directory to appear on disk. The
// scheduler fires due cron entries on every poll tick, so for sub-30s
// schedules the poll interval has to be lowered to match — otherwise the
// first fire of an @every 1s cron is delayed by up to 30 seconds. The test
// uses a 500ms poll interval to stay well inside the 10-second deadline.
func TestSchedulerFiresDAG(t *testing.T) {
	isolate(t)

	dagDir := t.TempDir()
	src, err := os.ReadFile(fixturePath(t, "scheduled.yaml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dagDir, "scheduled.yaml"), src, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	sch := scheduler.NewWithConfig(
		[]state.DAGSource{{Name: "test", Dir: dagDir}},
		state.SchedulerConfig{PollInterval: "500ms"},
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- sch.Start(ctx) }()

	// Poll for a run directory to show up. @every 1s should fire within
	// a couple of seconds; allow a generous window for CI noise.
	runsDir := filepath.Join(state.RunsDir(), "integration-scheduled")
	deadline := time.Now().Add(10 * time.Second)
	var found bool
	for time.Now().Before(deadline) {
		if entries, err := os.ReadDir(runsDir); err == nil {
			for _, date := range entries {
				if !date.IsDir() {
					continue
				}
				runs, _ := os.ReadDir(filepath.Join(runsDir, date.Name()))
				if len(runs) > 0 {
					found = true
					break
				}
			}
		}
		if found {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Error("scheduler did not exit within 10s of cancel")
	}

	if !found {
		t.Fatalf("no run directory appeared under %s within 10s", runsDir)
	}
}
