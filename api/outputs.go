package api

import (
	"net/http"
	"os"
	"strings"

	"github.com/cynkra/daggle/state"
)

func (s *Server) handleGetOutputs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	runID := r.PathValue("run_id")

	run, err := state.FindRun(name, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	events, err := state.ReadEvents(run.Dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read events: "+err.Error())
		return
	}

	// Collect outputs from completed steps by reading the accumulated env vars
	// The outputs are stored as DAGGLE_OUTPUT_<STEP_ID>_<KEY> in the events
	// But actually, outputs are captured by the engine and not stored in events.
	// We need to read them from the run's metadata or reconstruct from env.
	//
	// Since outputs are passed as env vars between steps, and the engine stores
	// them in memory only, we need another approach. Let's read the step stdout
	// log files and parse output markers directly.
	var outputs []OutputEntry

	// Find all step IDs from events
	stepIDs := make(map[string]bool)
	for _, e := range events {
		if e.StepID != "" {
			stepIDs[e.StepID] = true
		}
	}

	// Parse output markers from each step's stdout log
	for stepID := range stepIDs {
		markers := ParseOutputMarkers(run.Dir, stepID)
		for k, v := range markers {
			outputs = append(outputs, OutputEntry{
				StepID: stepID,
				Key:    k,
				Value:  v,
			})
		}
	}

	if outputs == nil {
		outputs = []OutputEntry{}
	}
	writeJSON(w, http.StatusOK, outputs)
}

// ParseOutputMarkers reads a step's stdout log and extracts ::daggle-output:: markers.
func ParseOutputMarkers(runDir, stepID string) map[string]string {
	data, err := os.ReadFile(runDir + "/" + stepID + ".stdout.log")
	if err != nil {
		return nil
	}

	outputs := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "::daggle-output name=") {
			continue
		}
		// Format: ::daggle-output name=key::value
		rest := strings.TrimPrefix(line, "::daggle-output name=")
		idx := strings.Index(rest, "::")
		if idx < 0 {
			continue
		}
		key := rest[:idx]
		value := strings.TrimSpace(rest[idx+2:])
		outputs[key] = value
	}
	return outputs
}
