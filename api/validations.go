package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (s *Server) handleGetValidations(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	runID := r.PathValue("run_id")

	run, err := s.findRun(name, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	entries, err := os.ReadDir(run.Dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read run dir: "+err.Error())
		return
	}

	var result []ValidationEntry
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".validations.json") {
			continue
		}
		stepID := strings.TrimSuffix(entry.Name(), ".validations.json")
		data, err := os.ReadFile(filepath.Join(run.Dir, entry.Name()))
		if err != nil {
			continue
		}

		var items []struct {
			Name    string `json:"name"`
			Status  string `json:"status"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(data, &items); err != nil {
			continue
		}
		for _, item := range items {
			result = append(result, ValidationEntry{
				StepID:  stepID,
				Name:    item.Name,
				Status:  item.Status,
				Message: item.Message,
			})
		}
	}

	if result == nil {
		result = []ValidationEntry{}
	}
	writeJSON(w, http.StatusOK, result)
}
