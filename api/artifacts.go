package api

import (
	"net/http"

	"github.com/cynkra/daggle/state"
)

func (s *Server) handleGetArtifacts(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	runID := r.PathValue("run_id")

	run, err := s.findRun(name, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	events, err := state.ReadEvents(run.Dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read events: "+err.Error())
		return
	}

	var artifacts []ArtifactEntry
	for _, e := range events {
		if e.Type == state.EventStepArtifact {
			artifacts = append(artifacts, ArtifactEntry{
				StepID:  e.StepID,
				Name:    e.ArtifactName,
				Path:    e.ArtifactPath,
				AbsPath: e.ArtifactAbsPath,
				Hash:    e.ArtifactHash,
				Size:    e.ArtifactSize,
				Format:  e.ArtifactFormat,
			})
		}
	}

	if artifacts == nil {
		artifacts = []ArtifactEntry{}
	}
	writeJSON(w, http.StatusOK, artifacts)
}
