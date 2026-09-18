package api

import (
	"context"
	_ "embed"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/cynkra/daggle/dag"
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
	schedules       ScheduleManager
	version         string
	started         time.Time
	logger          *slog.Logger
	ctx             context.Context
	cancel          context.CancelFunc
	auth            Auth
	trustProxy      bool
	basePath        string
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

// WithBasePath mounts the API and UI under a sub-path such as "/daggle", for
// reverse proxies that forward the prefix rather than stripping it.
//
// Routes are registered at their canonical paths and the prefix is stripped on
// the way in, so every handler and every route pattern stays prefix-unaware.
// Links the server emits are prefixed on the way out (see Server.Link).
func WithBasePath(p string) ServerOption {
	return func(s *Server) {
		s.basePath = normalizeBasePath(p)
	}
}

// normalizeBasePath turns operator input into a canonical prefix: empty, or a
// leading slash with no trailing slash. "/daggle/", "daggle" and "/daggle"
// all mean the same thing.
func normalizeBasePath(p string) string {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == "" {
		return ""
	}
	return "/" + p
}

// BasePath returns the configured sub-path prefix, or "" when the server is
// mounted at the root.
func (s *Server) BasePath() string { return s.basePath }

// Link prefixes a server-absolute path with the base path. Templates and
// redirects build every internal URL through this, so a sub-path deployment
// needs no separate link table.
func (s *Server) Link(p string) string { return s.basePath + p }

// publicPaths are reachable without credentials. Only the liveness probe
// qualifies: a container healthcheck has to work before anyone has a token,
// and it reveals nothing but that the process is up.
var publicPaths = map[string]bool{"/healthz": true}

// sources returns the current DAG sources.
func (s *Server) sources() []state.DAGSource {
	return s.sourceFunc()
}

// Handler returns the HTTP handler for the API server, wrapped in the
// configured middleware.
//
// Order matters. Forwarded headers are applied first so everything inside
// sees the client's real scheme and address; the base path is stripped next so
// auth and routing work on canonical paths; auth runs last before the mux so
// no route can be reached without passing it.
func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
	h = s.auth.requireAuth(h, publicPaths)
	h = s.stripBasePath(h)
	if s.trustProxy {
		h = forwarded(h)
	}
	return h
}

// stripBasePath removes the configured prefix before routing, and redirects
// the bare prefix ("/daggle") to its directory form ("/daggle/") so relative
// links from the UI resolve correctly.
func (s *Server) stripBasePath(next http.Handler) http.Handler {
	if s.basePath == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == s.basePath:
			http.Redirect(w, r, s.basePath+"/", http.StatusMovedPermanently)
		case strings.HasPrefix(r.URL.Path, s.basePath+"/"):
			http.StripPrefix(s.basePath, next).ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
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
	s.mux.HandleFunc("GET /healthz", handleLiveness)

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

	// Schedules
	s.mux.HandleFunc("GET /api/v1/dags/{name}/schedules", s.handleListSchedules)
	s.mux.HandleFunc("POST /api/v1/dags/{name}/schedules", s.handleCreateSchedule)
	s.mux.HandleFunc("DELETE /api/v1/dags/{name}/schedules/{schedule_id}", s.handleDeleteSchedule)
	s.mux.HandleFunc("PATCH /api/v1/dags/{name}/schedules/{schedule_id}", s.handlePatchSchedule)

	// Projects
	s.mux.HandleFunc("GET /api/v1/projects", s.handleListProjects)
	s.mux.HandleFunc("POST /api/v1/projects", s.handleRegisterProject)
	s.mux.HandleFunc("DELETE /api/v1/projects/{name}", s.handleUnregisterProject)

	// Maintenance
	s.mux.HandleFunc("POST /api/v1/runs/cleanup", s.handleCleanup)

	// UI
	s.registerUI()
}

// handleLiveness is the unauthenticated probe. It deliberately reports
// nothing but that the process is serving: version, uptime and run counts
// live on /api/v1/health, behind auth.
func handleLiveness(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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

// maxRequestBodyBytes caps the size of any JSON request body the API will
// accept. 1 MB comfortably covers all current request shapes (trigger params,
// project register, schedule create/patch, annotations, cancel reason) while
// preventing a single multi-GB POST from exhausting memory on a loopback
// attacker. Bumped per-handler if a future endpoint genuinely needs more.
const maxRequestBodyBytes int64 = 1 << 20

// readJSON decodes a JSON request body into v, refusing requests larger
// than maxRequestBodyBytes. Callers must pass the http.ResponseWriter so
// http.MaxBytesReader can return a 413 cleanly when the limit is hit.
func readJSON(w http.ResponseWriter, r *http.Request, v interface{}) error {
	if r.Body == nil {
		return nil
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	defer func() { _ = r.Body.Close() }()
	return json.NewDecoder(r.Body).Decode(v)
}

// runRedactor returns a Redactor for the DAG with the given name. Used by
// view-time handlers (step log, SSE stream) that need to mask secret values
// in subprocess output before serving. Falls back to a no-op redactor if the
// DAG can't be loaded or its env can't be resolved (e.g. vault unreachable);
// the resolution error is logged at warn level but does not fail the response.
func (s *Server) runRedactor(name string) *dag.Redactor {
	path := s.dagPath(name)
	if path == "" {
		return &dag.Redactor{}
	}
	r, err := dag.LoadRedactor(path)
	if err != nil {
		s.logger.Warn("redactor load failed; serving unredacted", "dag", name, "error", err)
	}
	return r
}

// dagPath resolves a DAG YAML file path by matching the parsed `name:` field.
// Returns the empty string when no source contains a DAG with that name.
//
// This walks every YAML file in every source and parses it via the DAG parse
// cache. After the first call the cache makes this O(sources) on the hot
// path; the canonical run-dir key (d.Name) drives all routing so we cannot
// fall back to filename-based lookup.
func (s *Server) dagPath(name string) string {
	if name == "" {
		return ""
	}
	var found string
	stop := errFoundDAG
	_ = state.WalkDAGFiles(s.sources(), func(src state.DAGSource, path string) error {
		cleanSrc := filepath.Clean(src.Dir) + string(filepath.Separator)
		if !strings.HasPrefix(filepath.Clean(path), cleanSrc) {
			return nil // path traversal guard
		}
		d, err := dag.ParseFileCached(path)
		if err != nil {
			return nil
		}
		if d.Name == name {
			found = path
			return stop
		}
		return nil
	})
	return found
}

// errFoundDAG is a sentinel passed to WalkDAGFiles to short-circuit the walk
// once a matching DAG is located.
var errFoundDAG = stopWalk{}

type stopWalk struct{}

func (stopWalk) Error() string { return "stop" }
