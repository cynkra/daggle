package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/cynkra/daggle/scheduler"
	"github.com/cynkra/daggle/state"
)

func (s *Server) handleListProjects(w http.ResponseWriter, _ *http.Request) {
	projects, err := state.LoadProjects()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load projects: "+err.Error())
		return
	}

	result := []ProjectSummary{}

	// Global dags directory
	globalDir := filepath.Join(state.ConfigDir(), "dags")
	globalStatus := "ok"
	if _, err := os.Stat(globalDir); os.IsNotExist(err) {
		globalStatus = "missing"
	}
	result = append(result, ProjectSummary{
		Name:   "(global)",
		Path:   globalDir,
		Status: globalStatus,
		DAGs:   countDAGs(globalDir),
	})

	// Registered projects
	for _, p := range projects {
		dagDir := filepath.Join(p.Path, ".daggle")
		status := "ok"
		if _, err := os.Stat(dagDir); os.IsNotExist(err) {
			status = "missing"
		}
		result = append(result, ProjectSummary{
			Name:   p.Name,
			Path:   p.Path,
			Status: status,
			DAGs:   countDAGs(dagDir),
		})
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleRegisterProject(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	absPath, err := filepath.Abs(req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid path: "+err.Error())
		return
	}

	// Validate the path exists and is a directory
	info, err := os.Stat(absPath)
	if err != nil {
		switch {
		case os.IsNotExist(err):
			writeError(w, http.StatusBadRequest, "path does not exist: "+absPath)
		case os.IsPermission(err):
			writeError(w, http.StatusForbidden, "cannot access path: "+absPath)
		default:
			writeError(w, http.StatusBadRequest, "cannot stat path: "+err.Error())
		}
		return
	}
	if !info.IsDir() {
		writeError(w, http.StatusBadRequest, "path is not a directory: "+absPath)
		return
	}

	// Check that the project has a .daggle/ directory
	dagDir := filepath.Join(absPath, ".daggle")
	dagInfo, err := os.Stat(dagDir)
	if err != nil || !dagInfo.IsDir() {
		writeError(w, http.StatusBadRequest, "no .daggle/ directory found in "+absPath+". Create it first with: mkdir -p "+dagDir)
		return
	}

	name := req.Name
	if name == "" {
		name = filepath.Base(absPath)
	}

	// Check for DAG name collisions
	existingSources := state.BuildDAGSources()
	var filteredSources []state.DAGSource
	for _, src := range existingSources {
		if src.Dir != dagDir {
			filteredSources = append(filteredSources, src)
		}
	}
	existingNames := state.CollectDAGNames(filteredSources)
	newNames := state.CollectDAGNames([]state.DAGSource{{Name: name, Dir: dagDir}})

	for dagName := range newNames {
		if owner, exists := existingNames[dagName]; exists {
			writeError(w, http.StatusConflict, "DAG "+dagName+" already exists in project "+owner)
			return
		}
	}

	if err := state.RegisterProject(name, absPath); err != nil {
		if strings.Contains(err.Error(), "already registered") {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	notifyScheduler(s.logger)

	writeJSON(w, http.StatusCreated, RegisterResponse{
		Name: name,
		Path: absPath,
	})
}

func (s *Server) handleUnregisterProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if err := state.UnregisterProject(name); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	notifyScheduler(s.logger)

	writeJSON(w, http.StatusOK, UnregisterResponse{Name: name})
}

// countDAGs returns the number of DAG files in a directory.
func countDAGs(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if n == "base.yaml" || n == "base.yml" {
			continue
		}
		if strings.HasSuffix(n, ".yaml") || strings.HasSuffix(n, ".yml") {
			count++
		}
	}
	return count
}

// notifyScheduler sends SIGHUP to a running scheduler to trigger DAG reload.
func notifyScheduler(logger interface{ Info(string, ...any) }) {
	pid, err := scheduler.ReadPID()
	if err != nil {
		return
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return
	}
	if err := syscall.Kill(pid, syscall.SIGHUP); err == nil {
		logger.Info("scheduler notified to reload")
	}
}
