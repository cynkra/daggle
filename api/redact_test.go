package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cynkra/daggle/state"
)

// setupRedactTestServer writes a DAG that references a secret resolved from
// MY_TEST_SECRET in the process env, and returns the server plus the secret
// value so tests can write log files containing it.
func setupRedactTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dagDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", dataDir)
	const secret = "supersecret-token-XYZ"
	t.Setenv("MY_TEST_SECRET", secret)

	dagYAML := `name: redact-dag
env:
  TOKEN:
    value: "${env:MY_TEST_SECRET}"
    secret: true
steps:
  - id: extract
    command: "echo hi"
`
	if err := os.WriteFile(filepath.Join(dagDir, "redact-dag.yaml"), []byte(dagYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := New(func() []state.DAGSource { return []state.DAGSource{{Name: "test", Dir: dagDir}} }, "test")
	return srv, secret
}

func TestHandleStepLog_RedactsSecrets(t *testing.T) {
	srv, secret := setupRedactTestServer(t)

	run, err := state.CreateRun("redact-dag")
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	stdoutLine := "connected with token=" + secret
	stderrLine := "Error: bad creds " + secret
	if err := os.WriteFile(filepath.Join(run.Dir, "extract.stdout.log"), []byte(stdoutLine), 0o644); err != nil {
		t.Fatalf("write stdout: %v", err)
	}
	if err := os.WriteFile(filepath.Join(run.Dir, "extract.stderr.log"), []byte(stderrLine), 0o644); err != nil {
		t.Fatalf("write stderr: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/dags/redact-dag/runs/"+run.ID+"/steps/extract/log", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var got StepLog
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if strings.Contains(got.Stdout, secret) {
		t.Errorf("stdout still contains secret: %q", got.Stdout)
	}
	if strings.Contains(got.Stderr, secret) {
		t.Errorf("stderr still contains secret: %q", got.Stderr)
	}
	if !strings.Contains(got.Stdout, "***") || !strings.Contains(got.Stderr, "***") {
		t.Errorf("expected *** masking; stdout=%q stderr=%q", got.Stdout, got.Stderr)
	}
}

func TestHandleStream_RedactsSecrets(t *testing.T) {
	srv, secret := setupRedactTestServer(t)

	run, err := state.CreateRun("redact-dag")
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	// Write events directly via NewEventWriter — this path bypasses the
	// engine's redaction (it's used by annotations / approvals), so the
	// secret will reach disk verbatim. The SSE handler must still mask it.
	w := state.NewEventWriter(run.Dir)
	_ = w.Write(state.Event{Type: state.EventRunStarted})
	_ = w.Write(state.Event{
		Type:   state.EventRunAnnotated,
		Note:   "leaked: " + secret,
		Author: "tester",
	})
	_ = w.Write(state.Event{Type: state.EventRunCompleted})

	oldInterval := streamPollInterval
	streamPollInterval = 20 * time.Millisecond
	t.Cleanup(func() { streamPollInterval = oldInterval })

	req := httptest.NewRequest("GET", "/api/v1/dags/redact-dag/runs/"+run.ID+"/stream", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if strings.Contains(body, secret) {
		t.Errorf("SSE body leaked secret:\n%s", body)
	}
	if !strings.Contains(body, "***") {
		t.Errorf("SSE body missing *** masking:\n%s", body)
	}
}
