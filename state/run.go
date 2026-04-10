package state

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rs/xid"
)

// RunInfo holds metadata about a single DAG run.
type RunInfo struct {
	ID        string
	DAGName   string
	StartTime time.Time
	Dir       string // absolute path to run directory
}

// CreateRun initializes a new run directory and returns its RunInfo.
func CreateRun(dagName string) (*RunInfo, error) {
	now := time.Now()
	id := xid.New().String()

	dir := filepath.Join(
		RunsDir(),
		dagName,
		now.Format("2006-01-02"),
		"run_"+id,
	)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create run directory: %w", err)
	}

	return &RunInfo{
		ID:        id,
		DAGName:   dagName,
		StartTime: now,
		Dir:       dir,
	}, nil
}

// ListRuns returns all runs for a DAG, sorted by time descending.
func ListRuns(dagName string) ([]RunInfo, error) {
	dagRunsDir := filepath.Join(RunsDir(), dagName)
	if _, err := os.Stat(dagRunsDir); os.IsNotExist(err) {
		return nil, nil
	}

	var runs []RunInfo

	// Walk date directories
	dateDirs, err := os.ReadDir(dagRunsDir)
	if err != nil {
		return nil, err
	}

	for _, dateDir := range dateDirs {
		if !dateDir.IsDir() {
			continue
		}
		datePath := filepath.Join(dagRunsDir, dateDir.Name())
		runDirs, err := os.ReadDir(datePath)
		if err != nil {
			continue
		}
		for _, runDir := range runDirs {
			if !runDir.IsDir() || !strings.HasPrefix(runDir.Name(), "run_") {
				continue
			}
			id := strings.TrimPrefix(runDir.Name(), "run_")
			dir := filepath.Join(datePath, runDir.Name())

			// Parse the xid to get the timestamp
			xidVal, err := xid.FromString(id)
			var startTime time.Time
			if err == nil {
				startTime = xidVal.Time()
			}

			runs = append(runs, RunInfo{
				ID:        id,
				DAGName:   dagName,
				StartTime: startTime,
				Dir:       dir,
			})
		}
	}

	sort.Slice(runs, func(i, j int) bool {
		return runs[i].StartTime.After(runs[j].StartTime)
	})

	return runs, nil
}

// LatestRun returns the most recent run for a DAG, or nil if none exist.
func LatestRun(dagName string) (*RunInfo, error) {
	runs, err := ListRuns(dagName)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, nil
	}
	return &runs[0], nil
}

// FindRun resolves a run by ID. If runID is "latest" or empty, returns the
// most recent run. Otherwise searches for a run matching the given ID.
func FindRun(dagName, runID string) (*RunInfo, error) {
	if runID == "" || runID == "latest" {
		run, err := LatestRun(dagName)
		if err != nil {
			return nil, fmt.Errorf("list runs for %q: %w", dagName, err)
		}
		if run == nil {
			return nil, fmt.Errorf("no runs found for DAG %q", dagName)
		}
		return run, nil
	}
	runs, err := ListRuns(dagName)
	if err != nil {
		return nil, fmt.Errorf("list runs for %q: %w", dagName, err)
	}
	for _, r := range runs {
		if r.ID == runID {
			return &r, nil
		}
	}
	return nil, fmt.Errorf("run %q not found for DAG %q", runID, dagName)
}

// RunStatus determines the final status of a run by reading its events.
func RunStatus(runDir string) string {
	events, err := ReadEvents(runDir)
	if err != nil || len(events) == 0 {
		return "unknown"
	}
	// Check for waiting approval state
	hasWaiting := false
	for _, e := range events {
		if e.Type == EventStepWaitApproval {
			hasWaiting = true
		}
		if e.Type == EventStepApproved || e.Type == EventStepRejected {
			hasWaiting = false
		}
	}

	last := events[len(events)-1]
	switch last.Type {
	case EventRunCompleted:
		return "completed"
	case EventRunFailed:
		return "failed"
	default:
		if hasWaiting {
			return "waiting"
		}
		return "running"
	}
}
