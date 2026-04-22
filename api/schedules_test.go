package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cynkra/daggle/scheduler"
)

// fakeScheduleManager is an in-memory stand-in for the real *scheduler.Scheduler.
// It records calls and lets tests assert the arguments the handlers pass in.
type fakeScheduleManager struct {
	list      []scheduler.ScheduleView
	addCalls  []addCall
	removeErr error
	patchErr  error

	lastAdd    addCall
	lastRemove removeCall
	lastPatch  patchCall
}

type addCall struct {
	dag, cron string
	params    map[string]string
	enabled   bool
}
type removeCall struct{ dag, id string }
type patchCall struct {
	dag, id string
	enabled bool
}

func (f *fakeScheduleManager) ListSchedules(_ string) []scheduler.ScheduleView {
	return f.list
}
func (f *fakeScheduleManager) AddRuntimeSchedule(dagName, cronExpr string, params map[string]string, enabled bool) (scheduler.ScheduleView, error) {
	f.lastAdd = addCall{dag: dagName, cron: cronExpr, params: params, enabled: enabled}
	f.addCalls = append(f.addCalls, f.lastAdd)
	return scheduler.ScheduleView{
		ID:      "sch_test",
		Cron:    cronExpr,
		Source:  "runtime",
		Enabled: enabled,
		NextRun: time.Date(2026, 4, 22, 9, 0, 0, 0, time.UTC),
		Params:  params,
	}, nil
}
func (f *fakeScheduleManager) RemoveRuntimeSchedule(dagName, scheduleID string) error {
	f.lastRemove = removeCall{dag: dagName, id: scheduleID}
	return f.removeErr
}
func (f *fakeScheduleManager) SetRuntimeScheduleEnabled(dagName, scheduleID string, enabled bool) (scheduler.ScheduleView, error) {
	f.lastPatch = patchCall{dag: dagName, id: scheduleID, enabled: enabled}
	if f.patchErr != nil {
		return scheduler.ScheduleView{}, f.patchErr
	}
	return scheduler.ScheduleView{ID: scheduleID, Source: "runtime", Enabled: enabled, Cron: "@every 1h"}, nil
}

func setupSchedulesServer(t *testing.T, mgr ScheduleManager) *Server {
	t.Helper()
	base, _ := setupTestServer(t)
	// Re-wire with the schedule manager — setupTestServer doesn't install one.
	// Capture base.sourceFunc directly so we don't accidentally recurse.
	return New(base.sourceFunc, "test-version", WithScheduleManager(mgr))
}

func TestSchedules_ListEmpty(t *testing.T) {
	mgr := &fakeScheduleManager{list: nil}
	srv := setupSchedulesServer(t, mgr)

	req := httptest.NewRequest("GET", "/api/v1/dags/test-dag/schedules", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got []Schedule
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("want empty list, got %v", got)
	}
}

func TestSchedules_ListMixed(t *testing.T) {
	mgr := &fakeScheduleManager{list: []scheduler.ScheduleView{
		{ID: "yaml-test-dag", Cron: "@every 1h", Source: "yaml", Enabled: true},
		{ID: "sch_runtime1", Cron: "0 7 * * *", Source: "runtime", Enabled: true, Params: map[string]string{"env": "prod"}, NextRun: time.Date(2026, 4, 23, 7, 0, 0, 0, time.UTC)},
	}}
	srv := setupSchedulesServer(t, mgr)

	req := httptest.NewRequest("GET", "/api/v1/dags/test-dag/schedules", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got []Schedule
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
	if got[0].Source != "yaml" || got[1].Source != "runtime" {
		t.Errorf("sources: %+v", got)
	}
	if got[1].NextRun == "" {
		t.Errorf("next_run should be set: %+v", got[1])
	}
	if got[1].Params["env"] != "prod" {
		t.Errorf("params: %+v", got[1])
	}
}

func TestSchedules_ListUnknownDAG(t *testing.T) {
	mgr := &fakeScheduleManager{}
	srv := setupSchedulesServer(t, mgr)

	req := httptest.NewRequest("GET", "/api/v1/dags/no-such-dag/schedules", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestSchedules_CreateValid(t *testing.T) {
	mgr := &fakeScheduleManager{}
	srv := setupSchedulesServer(t, mgr)

	body := strings.NewReader(`{"cron":"@every 1h","enabled":true,"params":{"env":"prod"}}`)
	req := httptest.NewRequest("POST", "/api/v1/dags/test-dag/schedules", body)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if mgr.lastAdd.dag != "test-dag" || mgr.lastAdd.cron != "@every 1h" || mgr.lastAdd.params["env"] != "prod" || !mgr.lastAdd.enabled {
		t.Errorf("args: %+v", mgr.lastAdd)
	}
	var got Schedule
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "sch_test" || !got.Enabled || got.NextRun == "" {
		t.Errorf("resp: %+v", got)
	}
}

func TestSchedules_CreateEnabledDefaultTrue(t *testing.T) {
	mgr := &fakeScheduleManager{}
	srv := setupSchedulesServer(t, mgr)

	body := strings.NewReader(`{"cron":"@every 1h"}`)
	req := httptest.NewRequest("POST", "/api/v1/dags/test-dag/schedules", body)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	if !mgr.lastAdd.enabled {
		t.Errorf("enabled default should be true")
	}
}

func TestSchedules_CreateMissingCron(t *testing.T) {
	mgr := &fakeScheduleManager{}
	srv := setupSchedulesServer(t, mgr)

	body := strings.NewReader(`{"enabled":true}`)
	req := httptest.NewRequest("POST", "/api/v1/dags/test-dag/schedules", body)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestSchedules_Delete(t *testing.T) {
	mgr := &fakeScheduleManager{}
	srv := setupSchedulesServer(t, mgr)

	req := httptest.NewRequest("DELETE", "/api/v1/dags/test-dag/schedules/sch_x", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", w.Code)
	}
	if mgr.lastRemove != (removeCall{dag: "test-dag", id: "sch_x"}) {
		t.Errorf("args: %+v", mgr.lastRemove)
	}
}

func TestSchedules_DeleteYAMLRejected(t *testing.T) {
	mgr := &fakeScheduleManager{removeErr: scheduler.ErrYAMLSchedule}
	srv := setupSchedulesServer(t, mgr)

	req := httptest.NewRequest("DELETE", "/api/v1/dags/test-dag/schedules/yaml-test-dag", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestSchedules_DeleteNotFound(t *testing.T) {
	mgr := &fakeScheduleManager{removeErr: &notFoundErr{"schedule not found"}}
	srv := setupSchedulesServer(t, mgr)

	req := httptest.NewRequest("DELETE", "/api/v1/dags/test-dag/schedules/sch_missing", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestSchedules_Patch(t *testing.T) {
	mgr := &fakeScheduleManager{}
	srv := setupSchedulesServer(t, mgr)

	body := strings.NewReader(`{"enabled":false}`)
	req := httptest.NewRequest("PATCH", "/api/v1/dags/test-dag/schedules/sch_x", body)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if mgr.lastPatch != (patchCall{dag: "test-dag", id: "sch_x", enabled: false}) {
		t.Errorf("args: %+v", mgr.lastPatch)
	}
	var got Schedule
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Enabled {
		t.Errorf("want disabled in response; got %+v", got)
	}
}

func TestSchedules_PatchMissingEnabled(t *testing.T) {
	mgr := &fakeScheduleManager{}
	srv := setupSchedulesServer(t, mgr)

	req := httptest.NewRequest("PATCH", "/api/v1/dags/test-dag/schedules/sch_x", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestSchedules_NoManager_ReturnsUnavailable(t *testing.T) {
	srv, _ := setupTestServer(t) // no WithScheduleManager

	for _, tc := range []struct {
		method, path string
	}{
		{"GET", "/api/v1/dags/test-dag/schedules"},
		{"POST", "/api/v1/dags/test-dag/schedules"},
		{"DELETE", "/api/v1/dags/test-dag/schedules/sch_x"},
		{"PATCH", "/api/v1/dags/test-dag/schedules/sch_x"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"cron":"@every 1h"}`))
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s: status = %d, want 503", tc.method, tc.path, w.Code)
		}
	}
}

// notFoundErr is a sentinel error shape used by the delete-not-found test,
// since the handler maps any non-ErrYAMLSchedule error to 404.
type notFoundErr struct{ msg string }

func (e *notFoundErr) Error() string { return e.msg }
