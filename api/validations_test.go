package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cynkra/daggle/state"
)

// setupValidationsFixture creates a DAG + run and returns the server, DAG name,
// run ID, and the run directory so tests can drop .validations.json files in.
func setupValidationsFixture(t *testing.T) (srv *Server, dag, runID, runDir string) {
	t.Helper()
	dagDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", dataDir)

	dagYAML := `name: demo-val
steps:
  - id: a
    command: echo a
`
	if err := os.WriteFile(filepath.Join(dagDir, "demo-val.yaml"), []byte(dagYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	run, err := state.CreateRun("demo-val")
	if err != nil {
		t.Fatal(err)
	}

	srv = New(func() []state.DAGSource { return []state.DAGSource{{Name: "test", Dir: dagDir}} }, "test-version")
	return srv, "demo-val", run.ID, run.Dir
}

func TestValidations_WellFormedFileIsReturned(t *testing.T) {
	srv, dag, runID, runDir := setupValidationsFixture(t)

	good := `[{"name":"row_count","status":"pass","message":"1000 rows"}]`
	if err := os.WriteFile(filepath.Join(runDir, "s1.validations.json"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/v1/dags/"+dag+"/runs/"+runID+"/validations", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var got []ValidationEntry
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].StepID != "s1" || got[0].Name != "row_count" || got[0].Status != "pass" {
		t.Errorf("got %+v, want one s1 row_count pass entry", got)
	}
}

// TestValidations_MalformedFileIsLoggedNotSwallowed confirms that a
// .validations.json file with unparseable JSON produces a slog warning (so
// operators see the problem) rather than silently dropping the entries for
// that step. The HTTP response still succeeds with the other valid entries.
func TestValidations_MalformedFileIsLoggedNotSwallowed(t *testing.T) {
	srv, dag, runID, runDir := setupValidationsFixture(t)

	// Drop one good and one malformed file into the run dir.
	good := `[{"name":"row_count","status":"pass","message":"1000 rows"}]`
	if err := os.WriteFile(filepath.Join(runDir, "good.validations.json"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := `{not valid json`
	if err := os.WriteFile(filepath.Join(runDir, "bad.validations.json"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	// Capture slog output so we can assert the warning was emitted.
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	req := httptest.NewRequest("GET", "/api/v1/dags/"+dag+"/runs/"+runID+"/validations", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", rr.Code, rr.Body.String())
	}

	// Response contains the valid entry; malformed file is skipped.
	var got []ValidationEntry
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].StepID != "good" {
		t.Errorf("got %+v, want one good entry (malformed file skipped)", got)
	}

	// Warning mentions the bad file so an operator can find it.
	logs := logBuf.String()
	if !strings.Contains(logs, "failed to parse validation file") {
		t.Errorf("expected warning about malformed file; log was: %s", logs)
	}
	if !strings.Contains(logs, "bad.validations.json") {
		t.Errorf("warning should name the malformed file; log was: %s", logs)
	}
}
