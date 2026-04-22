package state

import (
	"testing"
)

func TestSchedulesRoundTrip(t *testing.T) {
	t.Setenv("DAGGLE_CONFIG_DIR", t.TempDir())

	// Empty on fresh install.
	all, err := LoadSchedules()
	if err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("want empty, got %v", all)
	}

	// Add two schedules for two DAGs.
	if err := AddSchedule("dag-a", ScheduleEntry{ID: "s1", Cron: "0 7 * * *", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := AddSchedule("dag-a", ScheduleEntry{ID: "s2", Cron: "@hourly", Enabled: false, Params: map[string]string{"env": "prod"}}); err != nil {
		t.Fatal(err)
	}
	if err := AddSchedule("dag-b", ScheduleEntry{ID: "s3", Cron: "*/5 * * * *", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	// Reload and confirm.
	all, err = LoadSchedules()
	if err != nil {
		t.Fatal(err)
	}
	if len(all["dag-a"]) != 2 || len(all["dag-b"]) != 1 {
		t.Errorf("counts: dag-a=%d dag-b=%d", len(all["dag-a"]), len(all["dag-b"]))
	}
	// Params survived.
	if all["dag-a"][1].Params["env"] != "prod" {
		t.Errorf("params lost: %+v", all["dag-a"][1])
	}
}

func TestUpdateScheduleEnabled(t *testing.T) {
	t.Setenv("DAGGLE_CONFIG_DIR", t.TempDir())

	if err := AddSchedule("dag-a", ScheduleEntry{ID: "s1", Cron: "0 7 * * *", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	updated, err := UpdateScheduleEnabled("dag-a", "s1", false)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled {
		t.Errorf("want disabled, got enabled")
	}

	// And persisted.
	all, _ := LoadSchedules()
	if all["dag-a"][0].Enabled {
		t.Errorf("persistence: want disabled after reload")
	}

	// Not-found case.
	if _, err := UpdateScheduleEnabled("dag-a", "bogus", true); err == nil {
		t.Errorf("want error for unknown id")
	}
	if _, err := UpdateScheduleEnabled("no-such-dag", "s1", true); err == nil {
		t.Errorf("want error for unknown dag")
	}
}

func TestRemoveSchedule(t *testing.T) {
	t.Setenv("DAGGLE_CONFIG_DIR", t.TempDir())

	_ = AddSchedule("dag-a", ScheduleEntry{ID: "s1", Cron: "0 7 * * *", Enabled: true})
	_ = AddSchedule("dag-a", ScheduleEntry{ID: "s2", Cron: "@hourly", Enabled: true})

	if err := RemoveSchedule("dag-a", "s1"); err != nil {
		t.Fatal(err)
	}
	all, _ := LoadSchedules()
	if len(all["dag-a"]) != 1 || all["dag-a"][0].ID != "s2" {
		t.Errorf("remove wrong entry: %+v", all["dag-a"])
	}

	// Remove the last → DAG key drops out of the file.
	if err := RemoveSchedule("dag-a", "s2"); err != nil {
		t.Fatal(err)
	}
	all, _ = LoadSchedules()
	if _, ok := all["dag-a"]; ok {
		t.Errorf("want empty DAG pruned from map; got %+v", all)
	}

	// Not-found case.
	if err := RemoveSchedule("dag-a", "s1"); err == nil {
		t.Errorf("want error after the entry is gone")
	}
}
