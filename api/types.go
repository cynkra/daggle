package api

import "time"

// HealthResponse is returned by GET /api/v1/health.
type HealthResponse struct {
	Status        string         `json:"status"`
	Version       string         `json:"version"`
	UptimeSeconds float64        `json:"uptime_seconds"`
	Projects      int            `json:"projects"`
	DAGs          int            `json:"dags"`
	Scheduler     *SchedulerInfo `json:"scheduler,omitempty"`
	LastRun       *LastRunInfo   `json:"last_run,omitempty"`
}

// LastRunInfo holds info about the most recent run across all DAGs.
type LastRunInfo struct {
	DAGName string `json:"dag_name"`
	RunID   string `json:"run_id"`
	Status  string `json:"status"`
	Started string `json:"started"`
}

// DAGSummary is returned in the DAG list.
type DAGSummary struct {
	Name       string `json:"name"`
	Steps      int    `json:"steps"`
	Project    string `json:"project,omitempty"`
	Schedule   string `json:"schedule,omitempty"`
	LastStatus string `json:"last_status,omitempty"`
	LastRun    string `json:"last_run,omitempty"`
}

// DAGDetail is returned by GET /api/v1/dags/{name}.
type DAGDetail struct {
	Name       string   `json:"name"`
	Steps      int      `json:"steps"`
	StepIDs    []string `json:"step_ids"`
	Schedule   string   `json:"schedule,omitempty"`
	Workdir    string   `json:"workdir,omitempty"`
	RVersion   string   `json:"r_version,omitempty"`
	LastStatus string   `json:"last_status,omitempty"`
	LastRunID  string   `json:"last_run_id,omitempty"`
	LastRun    string   `json:"last_run,omitempty"`
}

// RunSummary is returned in the run list.
type RunSummary struct {
	RunID           string  `json:"run_id"`
	Started         string  `json:"started"`
	Status          string  `json:"status"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
	DAGHash         string  `json:"dag_hash,omitempty"`
}

// RunDetail is returned by GET /api/v1/dags/{name}/runs/{run_id}.
type RunDetail struct {
	RunID           string            `json:"run_id"`
	DAGName         string            `json:"dag_name"`
	Status          string            `json:"status"`
	Started         string            `json:"started"`
	Ended           string            `json:"ended,omitempty"`
	DurationSeconds float64           `json:"duration_seconds,omitempty"`
	DAGHash         string            `json:"dag_hash,omitempty"`
	RVersion        string            `json:"r_version,omitempty"`
	Platform        string            `json:"platform,omitempty"`
	Params          map[string]string `json:"params,omitempty"`
	Steps           []StepSummary     `json:"steps"`
}

// StepSummary is returned in step listings.
type StepSummary struct {
	StepID          string  `json:"step_id"`
	Status          string  `json:"status"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
	Attempts        int     `json:"attempts,omitempty"`
	Error           string  `json:"error,omitempty"`
	Message         string  `json:"message,omitempty"` // approval message
	Cached          bool    `json:"cached,omitempty"`
	CacheKey        string  `json:"cache_key,omitempty"`
}

// StepLog is returned by GET .../steps/{step_id}/log.
type StepLog struct {
	StepID string `json:"step_id"`
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

// OutputEntry is returned in the flat output list.
type OutputEntry struct {
	StepID string `json:"step_id"`
	Key    string `json:"key"`
	Value  string `json:"value"`
}

// ArtifactEntry is returned in the artifact list.
type ArtifactEntry struct {
	StepID  string `json:"step_id"`
	Name    string `json:"name"`
	Path    string `json:"path"`      // relative to workdir
	AbsPath string `json:"abs_path"`  // resolved absolute path
	Hash    string `json:"hash"`      // SHA-256
	Size    int64  `json:"size"`      // bytes
	Format  string `json:"format,omitempty"`
}

// TriggerRequest is the body for POST /api/v1/dags/{name}/run.
type TriggerRequest struct {
	Params map[string]string `json:"params,omitempty"`
}

// TriggerResponse is returned after triggering a run.
type TriggerResponse struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

// CleanupRequest is the body for POST /api/v1/runs/cleanup.
type CleanupRequest struct {
	OlderThan string `json:"older_than"` // e.g. "30d", "24h"
}

// CleanupResponse is returned after cleanup.
type CleanupResponse struct {
	Removed    int    `json:"removed"`
	FreedBytes int64  `json:"freed_bytes"`
	Freed      string `json:"freed"` // human-readable
}

// ProjectSummary is returned in the project list.
type ProjectSummary struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Status string `json:"status"` // "ok" or "missing"
	DAGs   int    `json:"dags"`
}

// RegisterRequest is the body for POST /api/v1/projects.
type RegisterRequest struct {
	Name string `json:"name,omitempty"` // optional, defaults to directory basename
	Path string `json:"path"`
}

// RegisterResponse is returned after registering a project.
type RegisterResponse struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// UnregisterResponse is returned after unregistering a project.
type UnregisterResponse struct {
	Name string `json:"name"`
}

// ErrorResponse is returned for errors.
type ErrorResponse struct {
	Error string `json:"error"`
}

// formatTime formats a time for JSON output, or returns empty string for zero time.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
