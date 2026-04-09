package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (s *Server) handleGetSummaries(w http.ResponseWriter, r *http.Request) {
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

	var summaries []SummaryEntry
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".summary.md") {
			continue
		}
		stepID := strings.TrimSuffix(entry.Name(), ".summary.md")
		data, err := os.ReadFile(filepath.Join(run.Dir, entry.Name()))
		if err != nil {
			continue
		}
		summaries = append(summaries, SummaryEntry{
			StepID:  stepID,
			Format:  "markdown",
			Content: string(data),
		})
	}

	if summaries == nil {
		summaries = []SummaryEntry{}
	}
	writeJSON(w, http.StatusOK, summaries)
}

func (s *Server) handleGetMetadata(w http.ResponseWriter, r *http.Request) {
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

	var result []RunMetaEntry
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta.json") {
			continue
		}
		stepID := strings.TrimSuffix(entry.Name(), ".meta.json")
		data, err := os.ReadFile(filepath.Join(run.Dir, entry.Name()))
		if err != nil {
			continue
		}

		var items []struct {
			Name  string `json:"name"`
			Type  string `json:"type"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(data, &items); err != nil {
			continue
		}
		for _, item := range items {
			result = append(result, RunMetaEntry{
				StepID: stepID,
				Name:   item.Name,
				Type:   item.Type,
				Value:  item.Value,
			})
		}
	}

	if result == nil {
		result = []RunMetaEntry{}
	}
	writeJSON(w, http.StatusOK, result)
}
