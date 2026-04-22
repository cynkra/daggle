package scheduler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cynkra/daggle/state"
)

// runtimeSchedulesSetup returns a Scheduler with a single registered DAG,
// a scratch DAGGLE_DATA_DIR, and a scratch DAGGLE_CONFIG_DIR — everything the
// runtime-schedule code needs to exercise persistence + cron registration
// without starting the main loop.
func runtimeSchedulesSetup(t *testing.T) *Scheduler {
	t.Helper()
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0o755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)
	t.Setenv("DAGGLE_CONFIG_DIR", filepath.Join(tmpDir, "config"))

	// A no-trigger DAG is enough — runtime schedules don't need YAML triggers.
	if err := os.WriteFile(filepath.Join(dagDir, "test-dag.yaml"), []byte(`
name: test-dag
steps:
  - id: hello
    command: echo hello
`), 0o644); err != nil {
		t.Fatal(err)
	}

	sources := []state.DAGSource{{Name: "test", Dir: dagDir}}
	return New(sources)
}

func TestAddRuntimeSchedule_EnabledRegistersWithCron(t *testing.T) {
	s := runtimeSchedulesSetup(t)

	view, err := s.AddRuntimeSchedule("test-dag", "@every 1h", map[string]string{"env": "prod"}, true)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if view.ID == "" || view.Cron != "@every 1h" || view.Source != "runtime" || !view.Enabled {
		t.Errorf("bad view: %+v", view)
	}
	if view.NextRun.IsZero() {
		t.Errorf("next_run is zero; want populated when enabled")
	}

	// Persisted.
	all, err := state.LoadSchedules()
	if err != nil {
		t.Fatal(err)
	}
	if len(all["test-dag"]) != 1 || all["test-dag"][0].ID != view.ID {
		t.Errorf("not persisted: %+v", all)
	}

	// Registered with cron.
	s.mu.Lock()
	defer s.mu.Unlock()
	rs := s.runtimeSchedules[view.ID]
	if rs == nil || rs.cronID == 0 {
		t.Errorf("not registered with cron: rs=%+v", rs)
	}
}

func TestAddRuntimeSchedule_DisabledDoesNotRegister(t *testing.T) {
	s := runtimeSchedulesSetup(t)

	view, err := s.AddRuntimeSchedule("test-dag", "0 7 * * *", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if view.Enabled {
		t.Errorf("want disabled")
	}
	if !view.NextRun.IsZero() {
		t.Errorf("want zero next_run when disabled")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtimeSchedules[view.ID].cronID != 0 {
		t.Errorf("disabled schedule should not have a cronID")
	}
}

func TestAddRuntimeSchedule_InvalidCron(t *testing.T) {
	s := runtimeSchedulesSetup(t)

	_, err := s.AddRuntimeSchedule("test-dag", "not a cron expression", nil, true)
	if err == nil {
		t.Fatalf("want error for invalid cron")
	}

	// Nothing persisted on validation failure.
	all, _ := state.LoadSchedules()
	if len(all["test-dag"]) != 0 {
		t.Errorf("validation failure should not persist: %+v", all)
	}
}

func TestAddRuntimeSchedule_UnknownDAG(t *testing.T) {
	s := runtimeSchedulesSetup(t)

	if _, err := s.AddRuntimeSchedule("no-such-dag", "@every 1h", nil, true); err == nil {
		t.Errorf("want error for unregistered DAG")
	}
}

func TestRemoveRuntimeSchedule(t *testing.T) {
	s := runtimeSchedulesSetup(t)

	view, _ := s.AddRuntimeSchedule("test-dag", "@every 1h", nil, true)
	if err := s.RemoveRuntimeSchedule("test-dag", view.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}

	// Unregistered in memory + storage.
	s.mu.Lock()
	if _, ok := s.runtimeSchedules[view.ID]; ok {
		t.Errorf("memory: still present")
	}
	s.mu.Unlock()
	all, _ := state.LoadSchedules()
	if _, ok := all["test-dag"]; ok {
		t.Errorf("storage: DAG should be pruned, got %+v", all)
	}
}

func TestRemoveRuntimeSchedule_YAMLRejected(t *testing.T) {
	s := runtimeSchedulesSetup(t)

	if err := s.RemoveRuntimeSchedule("test-dag", "yaml-test-dag"); !errors.Is(err, ErrYAMLSchedule) {
		t.Errorf("want ErrYAMLSchedule, got %v", err)
	}
}

func TestSetRuntimeScheduleEnabled_Toggle(t *testing.T) {
	s := runtimeSchedulesSetup(t)

	view, _ := s.AddRuntimeSchedule("test-dag", "@every 1h", nil, true)

	// Disable.
	updated, err := s.SetRuntimeScheduleEnabled("test-dag", view.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled {
		t.Errorf("want disabled")
	}
	s.mu.Lock()
	if s.runtimeSchedules[view.ID].cronID != 0 {
		t.Errorf("cronID not cleared on disable")
	}
	s.mu.Unlock()

	// Re-enable.
	updated, err = s.SetRuntimeScheduleEnabled("test-dag", view.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Enabled {
		t.Errorf("want enabled")
	}
	s.mu.Lock()
	if s.runtimeSchedules[view.ID].cronID == 0 {
		t.Errorf("cronID not populated on re-enable")
	}
	s.mu.Unlock()
}

func TestListSchedules_MixesYAMLAndRuntime(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0o755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)
	t.Setenv("DAGGLE_CONFIG_DIR", filepath.Join(tmpDir, "config"))

	// DAG with YAML schedule.
	if err := os.WriteFile(filepath.Join(dagDir, "test-dag.yaml"), []byte(`
name: test-dag
trigger:
  schedule: "@every 1h"
steps:
  - id: hello
    command: echo hello
`), 0o644); err != nil {
		t.Fatal(err)
	}

	sources := []state.DAGSource{{Name: "test", Dir: dagDir}}
	s := New(sources)

	// syncDAGs populates the YAML-schedule registration.
	if err := s.syncDAGs(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Add a runtime schedule alongside it.
	view, err := s.AddRuntimeSchedule("test-dag", "0 8 * * *", nil, true)
	if err != nil {
		t.Fatal(err)
	}

	list := s.ListSchedules("test-dag")
	if len(list) != 2 {
		t.Fatalf("want 2 entries, got %d: %+v", len(list), list)
	}
	var sawYAML, sawRuntime bool
	for _, e := range list {
		switch e.Source {
		case "yaml":
			sawYAML = true
		case "runtime":
			sawRuntime = true
			if e.ID != view.ID {
				t.Errorf("runtime id mismatch: %s vs %s", e.ID, view.ID)
			}
		}
	}
	if !sawYAML || !sawRuntime {
		t.Errorf("missing source: yaml=%v runtime=%v", sawYAML, sawRuntime)
	}
}

func TestLoadRuntimeSchedules_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	dagDir := filepath.Join(tmpDir, "dags")
	_ = os.MkdirAll(dagDir, 0o755)
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)
	t.Setenv("DAGGLE_CONFIG_DIR", filepath.Join(tmpDir, "config"))

	if err := os.WriteFile(filepath.Join(dagDir, "test-dag.yaml"), []byte(`
name: test-dag
steps:
  - id: hello
    command: echo hello
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Seed storage before the scheduler starts.
	_ = state.AddSchedule("test-dag", state.ScheduleEntry{ID: "sch_seeded_enabled", Cron: "@every 1h", Enabled: true})
	_ = state.AddSchedule("test-dag", state.ScheduleEntry{ID: "sch_seeded_disabled", Cron: "@every 2h", Enabled: false})

	sources := []state.DAGSource{{Name: "test", Dir: dagDir}}
	s := New(sources)

	s.loadRuntimeSchedules()

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.runtimeSchedules) != 2 {
		t.Errorf("want 2 loaded, got %d", len(s.runtimeSchedules))
	}
	if s.runtimeSchedules["sch_seeded_enabled"].cronID == 0 {
		t.Errorf("enabled should be registered with cron")
	}
	if s.runtimeSchedules["sch_seeded_disabled"].cronID != 0 {
		t.Errorf("disabled should not be registered with cron")
	}
}
