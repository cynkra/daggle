package state

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ScheduleEntry is a single runtime-added cron schedule for a DAG. YAML-
// declared `trigger.schedule` entries are NOT stored here — they live in the
// DAG file and are read directly by the scheduler.
type ScheduleEntry struct {
	ID      string            `yaml:"id" json:"id"`
	Cron    string            `yaml:"cron" json:"cron"`
	Enabled bool              `yaml:"enabled" json:"enabled"`
	Params  map[string]string `yaml:"params,omitempty" json:"params,omitempty"`
}

type schedulesFile struct {
	Schedules map[string][]ScheduleEntry `yaml:"schedules"`
}

// SchedulesPath returns the path to the runtime schedules registry.
func SchedulesPath() string {
	return filepath.Join(ConfigDir(), "schedules.yaml")
}

// LoadSchedules reads the runtime schedule registry. Returns an empty map if
// the file does not yet exist — that is the expected fresh-install state, not
// an error.
func LoadSchedules() (map[string][]ScheduleEntry, error) {
	data, err := os.ReadFile(SchedulesPath())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string][]ScheduleEntry{}, nil
		}
		return nil, err
	}
	var f schedulesFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", SchedulesPath(), err)
	}
	if f.Schedules == nil {
		f.Schedules = map[string][]ScheduleEntry{}
	}
	return f.Schedules, nil
}

// SaveSchedules writes the runtime schedule registry, creating the parent
// directory on the fly. Empty lists are pruned from the map so the file stays
// tidy when the last schedule for a DAG is removed.
func SaveSchedules(m map[string][]ScheduleEntry) error {
	cleaned := make(map[string][]ScheduleEntry, len(m))
	for k, v := range m {
		if len(v) > 0 {
			cleaned[k] = v
		}
	}
	out := schedulesFile{Schedules: cleaned}
	data, err := yaml.Marshal(out)
	if err != nil {
		return err
	}
	dir := filepath.Dir(SchedulesPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(SchedulesPath(), data, 0o644)
}

// AddSchedule appends a new schedule for the given DAG and persists.
func AddSchedule(dagName string, entry ScheduleEntry) error {
	all, err := LoadSchedules()
	if err != nil {
		return err
	}
	all[dagName] = append(all[dagName], entry)
	return SaveSchedules(all)
}

// RemoveSchedule drops the schedule with the given id from the DAG's list.
// Returns a descriptive error if no match is found.
func RemoveSchedule(dagName, scheduleID string) error {
	all, err := LoadSchedules()
	if err != nil {
		return err
	}
	list, ok := all[dagName]
	if !ok {
		return fmt.Errorf("schedule %q not found for DAG %q", scheduleID, dagName)
	}
	idx := -1
	for i, e := range list {
		if e.ID == scheduleID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("schedule %q not found for DAG %q", scheduleID, dagName)
	}
	all[dagName] = append(list[:idx], list[idx+1:]...)
	return SaveSchedules(all)
}

// UpdateScheduleEnabled flips the `enabled` field on the matching entry and
// returns the updated record.
func UpdateScheduleEnabled(dagName, scheduleID string, enabled bool) (ScheduleEntry, error) {
	all, err := LoadSchedules()
	if err != nil {
		return ScheduleEntry{}, err
	}
	list, ok := all[dagName]
	if !ok {
		return ScheduleEntry{}, fmt.Errorf("schedule %q not found for DAG %q", scheduleID, dagName)
	}
	for i := range list {
		if list[i].ID == scheduleID {
			list[i].Enabled = enabled
			all[dagName] = list
			if err := SaveSchedules(all); err != nil {
				return ScheduleEntry{}, err
			}
			return list[i], nil
		}
	}
	return ScheduleEntry{}, fmt.Errorf("schedule %q not found for DAG %q", scheduleID, dagName)
}
