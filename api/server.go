package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cynkra/daggle/state"
)

// SourceFunc returns the current list of DAG sources.
// Called on each request so newly registered projects are picked up.
type SourceFunc func() []state.DAGSource

// Server is the daggle REST API server.
type Server struct {
	mux        *http.ServeMux
	sourceFunc SourceFunc
	version    string
	started    time.Time
	logger     *slog.Logger
}

// New creates a new API server. The sourceFunc is called on each request
// to get the current DAG sources, so newly registered projects appear
// without a restart.
func New(sourceFunc SourceFunc, version string) *Server {
	s := &Server{
		mux:        http.NewServeMux(),
		sourceFunc: sourceFunc,
		version:    version,
		started:    time.Now(),
		logger:     slog.Default(),
	}
	s.registerRoutes()
	return s
}

// sources returns the current DAG sources.
func (s *Server) sources() []state.DAGSource {
	return s.sourceFunc()
}

// Handler returns the HTTP handler for the API server.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) registerRoutes() {
	// System
	s.mux.HandleFunc("GET /api/v1/health", s.handleHealth)

	// DAGs
	s.mux.HandleFunc("GET /api/v1/dags", s.handleListDAGs)
	s.mux.HandleFunc("GET /api/v1/dags/{name}", s.handleGetDAG)
	s.mux.HandleFunc("POST /api/v1/dags/{name}/run", s.handleTriggerRun)

	// Runs
	s.mux.HandleFunc("GET /api/v1/dags/{name}/runs", s.handleListRuns)
	s.mux.HandleFunc("GET /api/v1/dags/{name}/runs/{run_id}", s.handleGetRun)
	s.mux.HandleFunc("POST /api/v1/dags/{name}/runs/{run_id}/cancel", s.handleCancelRun)

	// Steps & Approval
	s.mux.HandleFunc("GET /api/v1/dags/{name}/runs/{run_id}/steps", s.handleListSteps)
	s.mux.HandleFunc("GET /api/v1/dags/{name}/runs/{run_id}/steps/{step_id}/log", s.handleStepLog)
	s.mux.HandleFunc("POST /api/v1/dags/{name}/runs/{run_id}/steps/{step_id}/approve", s.handleApproveStep)
	s.mux.HandleFunc("POST /api/v1/dags/{name}/runs/{run_id}/steps/{step_id}/reject", s.handleRejectStep)

	// Outputs
	s.mux.HandleFunc("GET /api/v1/dags/{name}/runs/{run_id}/outputs", s.handleGetOutputs)

	// Maintenance
	s.mux.HandleFunc("POST /api/v1/runs/cleanup", s.handleCleanup)

	// UI
	s.registerUI()
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes an error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorResponse{Error: msg})
}

// readJSON decodes a JSON request body into v.
func readJSON(r *http.Request, v interface{}) error {
	if r.Body == nil {
		return nil
	}
	defer func() { _ = r.Body.Close() }()
	return json.NewDecoder(r.Body).Decode(v)
}

// dagPath resolves a DAG YAML file path from a name, searching all sources.
func (s *Server) dagPath(name string) string {
	for _, src := range s.sources() {
		cleanSrc := filepath.Clean(src.Dir) + string(filepath.Separator)
		for _, ext := range []string{".yaml", ".yml"} {
			p := filepath.Join(src.Dir, name+ext)
			if !strings.HasPrefix(filepath.Clean(p), cleanSrc) {
				continue // path traversal attempt
			}
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// parseDurationWithDays parses a duration string that may include "d" suffix for days.
func parseDurationWithDays(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		numStr := strings.TrimSuffix(s, "d")
		var days float64
		if _, err := fmt.Sscanf(numStr, "%f", &days); err != nil {
			return 0, fmt.Errorf("invalid day count %q", numStr)
		}
		return time.Duration(days * float64(24*time.Hour)), nil
	}
	return time.ParseDuration(s)
}
