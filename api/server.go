package api

import (
	"context"
	_ "embed"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cynkra/daggle/state"
)

//go:embed openapi.yaml
var openapiSpec []byte

// SourceFunc returns the current list of DAG sources.
// Called on each request so newly registered projects are picked up.
type SourceFunc func() []state.DAGSource

// SchedulerStatusFunc returns a snapshot of the scheduler's state.
// Nil if no scheduler is running (e.g. API-only mode).
type SchedulerStatusFunc func() *SchedulerInfo

// SchedulerInfo holds scheduler state exposed via the health endpoint.
type SchedulerInfo struct {
	RegisteredDAGs int            `json:"registered_dags"`
	ActiveRuns     int            `json:"active_runs"`
	MaxConcurrent  int            `json:"max_concurrent"`
	TriggerCounts  map[string]int `json:"trigger_counts,omitempty"`
}

// Server is the daggle REST API server.
type Server struct {
	mux             *http.ServeMux
	sourceFunc      SourceFunc
	schedulerStatus SchedulerStatusFunc
	version         string
	started         time.Time
	logger          *slog.Logger
	ctx             context.Context
	cancel          context.CancelFunc
}

// New creates a new API server. The sourceFunc is called on each request
// to get the current DAG sources, so newly registered projects appear
// without a restart.
func New(sourceFunc SourceFunc, version string, opts ...ServerOption) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		mux:        http.NewServeMux(),
		sourceFunc: sourceFunc,
		version:    version,
		started:    time.Now(),
		logger:     slog.Default(),
		ctx:        ctx,
		cancel:     cancel,
	}
	for _, opt := range opts {
		opt(s)
	}
	s.registerRoutes()
	return s
}

// ServerOption configures the API server.
type ServerOption func(*Server)

// WithSchedulerStatus provides a function to query scheduler state.
func WithSchedulerStatus(fn SchedulerStatusFunc) ServerOption {
	return func(s *Server) {
		s.schedulerStatus = fn
	}
}

// sources returns the current DAG sources.
func (s *Server) sources() []state.DAGSource {
	return s.sourceFunc()
}

// Handler returns the HTTP handler for the API server.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// Shutdown cancels the server context, signalling all async runs to stop.
func (s *Server) Shutdown() {
	s.cancel()
}

func (s *Server) registerRoutes() {
	// OpenAPI spec
	s.mux.HandleFunc("GET /openapi.yaml", handleOpenAPISpec)

	// System
	s.mux.HandleFunc("GET /api/v1/health", s.handleHealth)

	// DAGs
	s.mux.HandleFunc("GET /api/v1/dags", s.handleListDAGs)
	s.mux.HandleFunc("GET /api/v1/dags/{name}", s.handleGetDAG)
	s.mux.HandleFunc("POST /api/v1/dags/{name}/run", s.handleTriggerRun)
	s.mux.HandleFunc("GET /api/v1/dags/{name}/plan", s.handleGetPlan)
	s.mux.HandleFunc("GET /api/v1/dags/{name}/impact", s.handleGetImpact)

	// Runs
	s.mux.HandleFunc("GET /api/v1/dags/{name}/runs/compare", s.handleCompareRuns)
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

	// Summaries & Metadata
	s.mux.HandleFunc("GET /api/v1/dags/{name}/runs/{run_id}/summaries", s.handleGetSummaries)
	s.mux.HandleFunc("GET /api/v1/dags/{name}/runs/{run_id}/metadata", s.handleGetMetadata)

	// Validations
	s.mux.HandleFunc("GET /api/v1/dags/{name}/runs/{run_id}/validations", s.handleGetValidations)

	// Artifacts
	s.mux.HandleFunc("GET /api/v1/dags/{name}/runs/{run_id}/artifacts", s.handleGetArtifacts)

	// Annotations
	s.mux.HandleFunc("GET /api/v1/dags/{name}/runs/{run_id}/annotations", s.handleListAnnotations)
	s.mux.HandleFunc("POST /api/v1/dags/{name}/runs/{run_id}/annotations", s.handleAddAnnotation)

	// Live event streaming (SSE)
	s.mux.HandleFunc("GET /api/v1/dags/{name}/runs/{run_id}/stream", s.handleStream)

	// Archive
	s.mux.HandleFunc("POST /api/v1/dags/{name}/runs/{run_id}/archive", s.handleCreateArchive)
	s.mux.HandleFunc("GET /api/v1/dags/{name}/runs/{run_id}/archive", s.handleDownloadArchive)
	s.mux.HandleFunc("POST /api/v1/dags/{name}/runs/{run_id}/verify", s.handleVerifyArchive)

	// Projects
	s.mux.HandleFunc("GET /api/v1/projects", s.handleListProjects)
	s.mux.HandleFunc("POST /api/v1/projects", s.handleRegisterProject)
	s.mux.HandleFunc("DELETE /api/v1/projects/{name}", s.handleUnregisterProject)

	// Maintenance
	s.mux.HandleFunc("POST /api/v1/runs/cleanup", s.handleCleanup)

	// UI
	s.registerUI()
}

func handleOpenAPISpec(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openapiSpec)
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

