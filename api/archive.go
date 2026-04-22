package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cynkra/daggle/internal/archive"
	"github.com/cynkra/daggle/state"
)

// archivePath returns the canonical storage path for a run's archive on the
// data volume. Filename is fully determined by the resolved run ID (a xid),
// so two callers will always agree.
func archivePath(dagName, runID string) string {
	return filepath.Join(state.DataDir(), "archives", fmt.Sprintf("%s_%s.tar.gz", dagName, runID))
}

func (s *Server) handleCreateArchive(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	runID := r.PathValue("run_id")

	run, err := state.FindRun(name, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	out := archivePath(name, run.ID)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "create archive dir: "+err.Error())
		return
	}

	res, err := archive.Create(run.Dir, out)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "archive: "+err.Error())
		return
	}

	info, err := os.Stat(res.OutPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stat archive: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, ArchiveResponse{
		Path:      res.OutPath,
		Files:     res.Files,
		Bytes:     res.Bytes,
		CreatedAt: info.ModTime().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleDownloadArchive(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	runID := r.PathValue("run_id")

	run, err := state.FindRun(name, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	out := archivePath(name, run.ID)
	info, err := os.Stat(out)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "archive not found; POST to this endpoint to create it first")
			return
		}
		writeError(w, http.StatusInternalServerError, "stat archive: "+err.Error())
		return
	}
	if info.IsDir() {
		writeError(w, http.StatusInternalServerError, "archive path is a directory")
		return
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s_%s.tar.gz"`, name, run.ID))
	http.ServeFile(w, r, out)
}

func (s *Server) handleVerifyArchive(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	runID := r.PathValue("run_id")

	run, err := state.FindRun(name, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	out := archivePath(name, run.ID)
	if _, err := os.Stat(out); err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "archive not found; POST to /archive to create it first")
			return
		}
		writeError(w, http.StatusInternalServerError, "stat archive: "+err.Error())
		return
	}

	report, err := archive.Verify(out)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "verify: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, VerifyResponse{
		OK:         report.OK(),
		Files:      report.Files,
		Mismatched: nonNilStrings(report.Mismatched),
		Missing:    nonNilStrings(report.Missing),
		Extra:      nonNilStrings(report.Extra),
	})
}

// nonNilStrings returns s or an empty slice if nil, so JSON marshals to [] not null.
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
