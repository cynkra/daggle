package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMonitorCmd_MissingRun(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", dataDir)

	rootCmd.SetArgs([]string{"monitor", "no-such-dag"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when no run exists for the DAG")
	}
}

func TestResolveAPIBaseURL_ExplicitURL(t *testing.T) {
	got, stop, err := resolveAPIBaseURL("http://example.com/api/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stop != nil {
		t.Error("no embedded server should start when URL is explicit")
	}
	if got != "http://example.com/api" {
		t.Errorf("url = %q, want trailing slash stripped", got)
	}
}

func TestPingAPI(t *testing.T) {
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/health") {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer okSrv.Close()

	if !pingAPI(okSrv.URL) {
		t.Error("expected pingAPI=true for a server that serves /api/v1/health")
	}
	if pingAPI("http://127.0.0.1:1") {
		t.Error("expected pingAPI=false for a port with no listener")
	}
}
