package dag

import "testing"

func TestTriggerHelpers(t *testing.T) {
	// No trigger
	d1 := &DAG{Name: "no-trigger"}
	if d1.HasTrigger() {
		t.Error("expected HasTrigger()=false for nil trigger")
	}
	if d1.CronSchedule() != "" {
		t.Error("expected empty CronSchedule() for nil trigger")
	}

	// Schedule trigger
	d2 := &DAG{Name: "cron", Trigger: &Trigger{Schedule: "@every 1h"}}
	if !d2.HasTrigger() {
		t.Error("expected HasTrigger()=true for schedule trigger")
	}
	if d2.CronSchedule() != "@every 1h" {
		t.Errorf("CronSchedule() = %q, want %q", d2.CronSchedule(), "@every 1h")
	}

	// Watch trigger
	d3 := &DAG{Name: "watch", Trigger: &Trigger{Watch: &WatchTrigger{Path: "/data"}}}
	if !d3.HasTrigger() {
		t.Error("expected HasTrigger()=true for watch trigger")
	}
	if d3.CronSchedule() != "" {
		t.Error("expected empty CronSchedule() for watch-only trigger")
	}
}

func TestValidate_TriggerBlock(t *testing.T) {
	base := func() *DAG {
		return &DAG{
			Name:  "test",
			Steps: []Step{{ID: "a", Command: "echo a"}},
		}
	}

	// Valid: no trigger
	if err := Validate(base()); err != nil {
		t.Errorf("no trigger should be valid: %v", err)
	}

	// Valid: schedule trigger
	d := base()
	d.Trigger = &Trigger{Schedule: "@every 1h"}
	if err := Validate(d); err != nil {
		t.Errorf("schedule trigger should be valid: %v", err)
	}

	// Invalid: watch without path
	d = base()
	d.Trigger = &Trigger{Watch: &WatchTrigger{}}
	if err := Validate(d); err == nil {
		t.Error("watch without path should fail")
	}

	// Invalid: on_dag without name
	d = base()
	d.Trigger = &Trigger{OnDAG: &OnDAGTrigger{}}
	if err := Validate(d); err == nil {
		t.Error("on_dag without name should fail")
	}

	// Invalid: on_dag with bad status
	d = base()
	d.Trigger = &Trigger{OnDAG: &OnDAGTrigger{Name: "upstream", Status: "bogus"}}
	if err := Validate(d); err == nil {
		t.Error("on_dag with invalid status should fail")
	}

	// Invalid: condition without r_expr or command
	d = base()
	d.Trigger = &Trigger{Condition: &ConditionTrigger{}}
	if err := Validate(d); err == nil {
		t.Error("condition without r_expr or command should fail")
	}

	// Invalid: git with bad event
	d = base()
	d.Trigger = &Trigger{Git: &GitTrigger{Events: []string{"push", "invalid"}}}
	if err := Validate(d); err == nil {
		t.Error("git with invalid event should fail")
	}

	// Invalid: bad debounce duration
	d = base()
	d.Trigger = &Trigger{Watch: &WatchTrigger{Path: "/data", Debounce: "not-a-duration"}}
	if err := Validate(d); err == nil {
		t.Error("watch with invalid debounce should fail")
	}
}

func TestValidate_Deadline(t *testing.T) {
	base := func() *DAG {
		return &DAG{
			Name:  "test",
			Steps: []Step{{ID: "a", Command: "echo a"}},
		}
	}

	// Valid: deadline with on_deadline
	d := base()
	d.Trigger = &Trigger{
		Schedule:   "@every 1h",
		Deadline:   "08:00",
		OnDeadline: &Hook{Command: "echo missed"},
	}
	if err := Validate(d); err != nil {
		t.Errorf("valid deadline should pass: %v", err)
	}

	// Valid: deadline without on_deadline (just deadline, no hook)
	d = base()
	d.Trigger = &Trigger{Schedule: "@every 1h", Deadline: "23:59"}
	if err := Validate(d); err != nil {
		t.Errorf("deadline without on_deadline should be valid: %v", err)
	}

	// Invalid: single-digit hour "8:00"
	d = base()
	d.Trigger = &Trigger{Schedule: "@every 1h", Deadline: "8:00"}
	if err := Validate(d); err == nil {
		t.Error("deadline '8:00' should fail (must be HH:MM)")
	}

	// Invalid: hour out of range "25:00"
	d = base()
	d.Trigger = &Trigger{Schedule: "@every 1h", Deadline: "25:00"}
	if err := Validate(d); err == nil {
		t.Error("deadline '25:00' should fail (hour > 23)")
	}

	// Invalid: non-numeric "abc"
	d = base()
	d.Trigger = &Trigger{Schedule: "@every 1h", Deadline: "abc"}
	if err := Validate(d); err == nil {
		t.Error("deadline 'abc' should fail")
	}

	// Invalid: minute out of range "12:60"
	d = base()
	d.Trigger = &Trigger{Schedule: "@every 1h", Deadline: "12:60"}
	if err := Validate(d); err == nil {
		t.Error("deadline '12:60' should fail (minute > 59)")
	}

	// Invalid: on_deadline without deadline
	d = base()
	d.Trigger = &Trigger{
		Schedule:   "@every 1h",
		OnDeadline: &Hook{Command: "echo missed"},
	}
	if err := Validate(d); err == nil {
		t.Error("on_deadline without deadline should fail")
	}
}
