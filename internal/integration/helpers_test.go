//go:build integration

// Package integration holds end-to-end tests that wire multiple daggle
// packages together and exercise real R/shell subprocesses.
//
// Tests in this package are gated by the `integration` build tag so they
// do not run under the default `go test ./...` invocation. To run them:
//
//	go test -tags=integration ./internal/integration/...
//
// Most scenarios require a working `Rscript` on PATH. When CI=true the
// helpers fail loudly instead of skipping, so a missing R in CI surfaces
// as a red test rather than a silent pass.
package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/cynkra/daggle/cache"
	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/internal/engine"
	"github.com/cynkra/daggle/internal/executor"
	"github.com/cynkra/daggle/state"
)

// fixturePath returns the absolute path to a fixture YAML file.
func fixturePath(t *testing.T, name string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "fixtures", name)
}

// isolate points DAGGLE_DATA_DIR and DAGGLE_CONFIG_DIR at a fresh temp dir
// so each test gets its own runs tree and doesn't touch the user's XDG
// directories.
func isolate(t *testing.T) string {
	t.Helper()
	tmpData := t.TempDir()
	tmpConfig := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", tmpData)
	t.Setenv("DAGGLE_CONFIG_DIR", tmpConfig)
	return tmpData
}

// requireR ensures Rscript is on PATH. In CI (CI=true), the helper fails
// the test loudly; locally, it skips so developers without R can still
// run non-R scenarios.
func requireR(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("Rscript"); err != nil {
		if os.Getenv("CI") == "true" {
			t.Fatalf("Rscript not found on PATH but CI=true: %v", err)
		}
		t.Skipf("Rscript not found on PATH: %v", err)
	}
}

// loadDAG parses a fixture and applies template expansion.
func loadDAG(t *testing.T, fixture string, params map[string]string) *dag.DAG {
	t.Helper()
	d, err := dag.LoadAndExpand(fixturePath(t, fixture), params)
	if err != nil {
		t.Fatalf("LoadAndExpand(%s): %v", fixture, err)
	}
	return d
}

// runDAG creates a run directory, wires up a real engine with the real
// executor factory, and executes the DAG. Returns the run info plus the
// engine error (nil on success).
func runDAG(t *testing.T, d *dag.DAG) (*state.RunInfo, error) {
	t.Helper()
	return runDAGCtx(t, context.Background(), d)
}

// runDAGCtx is runDAG with an explicit context so callers can set a deadline.
func runDAGCtx(t *testing.T, ctx context.Context, d *dag.DAG) (*state.RunInfo, error) {
	t.Helper()
	run, err := state.CreateRun(d.Name)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	cacheDir := filepath.Join(state.DataDir(), "cache")
	cfg := engine.Config{
		DAG:         d,
		Run:         run,
		ExecFactory: executor.New,
		Meta: &state.RunMeta{
			RunID:     run.ID,
			DAGName:   d.Name,
			StartTime: time.Now(),
			Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		},
		CacheStore: cache.NewStore(cacheDir),
	}
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return run, eng.Run(ctx)
}

// readEvents reads the completed events.jsonl from a run directory.
// State reads are cached per-dir — if a test writes events externally,
// call state.InvalidateEventCache(run.Dir) first.
func readEvents(t *testing.T, run *state.RunInfo) []state.Event {
	t.Helper()
	state.InvalidateEventCache(run.Dir)
	events, err := state.ReadEvents(run.Dir)
	if err != nil {
		t.Fatalf("ReadEvents(%s): %v", run.Dir, err)
	}
	return events
}

// eventTypes extracts just the Type field from a slice of events, handy
// for order assertions.
func eventTypes(events []state.Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Type
	}
	return out
}

// findEvent returns the first event matching the type and step ID, or
// nil if none found.
func findEvent(events []state.Event, eventType, stepID string) *state.Event {
	for i := range events {
		if events[i].Type == eventType && events[i].StepID == stepID {
			return &events[i]
		}
	}
	return nil
}

// countEvents counts events whose Type matches any of the given types.
func countEvents(events []state.Event, types ...string) int {
	want := make(map[string]bool, len(types))
	for _, t := range types {
		want[t] = true
	}
	n := 0
	for _, e := range events {
		if want[e.Type] {
			n++
		}
	}
	return n
}
