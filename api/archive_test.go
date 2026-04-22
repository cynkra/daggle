package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	intarchive "github.com/cynkra/daggle/internal/archive"
	"github.com/cynkra/daggle/state"
)

// setupRunForArchive creates a run with a couple of regular files and returns
// its run ID plus the data directory. The server shares this data dir via
// DAGGLE_DATA_DIR (set in setupTestServer).
func setupRunForArchive(t *testing.T) (string, string) {
	t.Helper()
	run, err := state.CreateRun("test-dag")
	if err != nil {
		t.Fatal(err)
	}
	// Minimal content that will hash reproducibly.
	if err := os.WriteFile(filepath.Join(run.Dir, "events.jsonl"), []byte(`{"type":"run_started"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run.Dir, "meta.json"), []byte(`{"run_id":"`+run.ID+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return run.ID, state.DataDir()
}

func TestArchive_CreateAndDownload(t *testing.T) {
	srv, _ := setupTestServer(t)
	runID, dataDir := setupRunForArchive(t)

	// POST create
	postReq := httptest.NewRequest("POST", "/api/v1/dags/test-dag/runs/"+runID+"/archive", nil)
	postW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(postW, postReq)

	if postW.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, body = %s", postW.Code, postW.Body.String())
	}
	var resp ArchiveResponse
	if err := json.Unmarshal(postW.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	wantPath := filepath.Join(dataDir, "archives", "test-dag_"+runID+".tar.gz")
	if resp.Path != wantPath {
		t.Errorf("path = %q, want %q", resp.Path, wantPath)
	}
	if resp.Files != 2 {
		t.Errorf("files = %d, want 2", resp.Files)
	}
	if resp.Bytes <= 0 {
		t.Errorf("bytes = %d, want > 0", resp.Bytes)
	}
	if resp.CreatedAt == "" {
		t.Errorf("created_at is empty")
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("archive not at expected path: %v", err)
	}

	// GET download
	getReq := httptest.NewRequest("GET", "/api/v1/dags/test-dag/runs/"+runID+"/archive", nil)
	getW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", getW.Code, getW.Body.String())
	}
	if ct := getW.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("content-type = %q, want application/gzip", ct)
	}
	if cd := getW.Header().Get("Content-Disposition"); cd == "" {
		t.Errorf("content-disposition is empty")
	}

	// Body must be a valid gzip; manifest must be first entry.
	gz, err := gzip.NewReader(bytes.NewReader(getW.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	tr := tar.NewReader(gz)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("read first tar entry: %v", err)
	}
	if hdr.Name != intarchive.ManifestName {
		t.Errorf("first entry = %q, want %q", hdr.Name, intarchive.ManifestName)
	}
}

func TestArchive_VerifyDownloadedBytes(t *testing.T) {
	srv, _ := setupTestServer(t)
	runID, _ := setupRunForArchive(t)

	req := httptest.NewRequest("POST", "/api/v1/dags/test-dag/runs/"+runID+"/archive", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST: %d %s", w.Code, w.Body.String())
	}

	getReq := httptest.NewRequest("GET", "/api/v1/dags/test-dag/runs/"+runID+"/archive", nil)
	getW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getW, getReq)

	// Write downloaded bytes to disk and verify via the public archive.Verify.
	tmp := filepath.Join(t.TempDir(), "dl.tar.gz")
	if err := os.WriteFile(tmp, getW.Body.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := intarchive.Verify(tmp)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !report.OK() {
		t.Errorf("verify not OK: %+v", report)
	}
}

func TestArchive_CreateNonexistentRun(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/v1/dags/test-dag/runs/nonexistent/archive", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestArchive_DownloadBeforeCreate(t *testing.T) {
	srv, _ := setupTestServer(t)
	runID, _ := setupRunForArchive(t)

	req := httptest.NewRequest("GET", "/api/v1/dags/test-dag/runs/"+runID+"/archive", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestArchive_ReCreateOverwrites(t *testing.T) {
	srv, _ := setupTestServer(t)
	runID, dataDir := setupRunForArchive(t)

	// First archive.
	req1 := httptest.NewRequest("POST", "/api/v1/dags/test-dag/runs/"+runID+"/archive", nil)
	w1 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w1, req1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first POST: %d %s", w1.Code, w1.Body.String())
	}

	wantPath := filepath.Join(dataDir, "archives", "test-dag_"+runID+".tar.gz")
	size1, err := os.Stat(wantPath)
	if err != nil {
		t.Fatal(err)
	}

	// Add a file so the second archive is bigger.
	run, err := state.FindRun("test-dag", runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run.Dir, "extra.txt"), bytes.Repeat([]byte("x"), 2048), 0o644); err != nil {
		t.Fatal(err)
	}

	// Re-archive.
	req2 := httptest.NewRequest("POST", "/api/v1/dags/test-dag/runs/"+runID+"/archive", nil)
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, req2)
	if w2.Code != http.StatusCreated {
		t.Fatalf("second POST: %d %s", w2.Code, w2.Body.String())
	}

	size2, err := os.Stat(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if size2.Size() <= size1.Size() {
		t.Errorf("size2 (%d) <= size1 (%d); overwrite did not include new file", size2.Size(), size1.Size())
	}

	// Verify still OK.
	report, err := intarchive.Verify(wantPath)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !report.OK() {
		t.Errorf("verify not OK: %+v", report)
	}
}

func TestArchive_EmptyRun(t *testing.T) {
	srv, _ := setupTestServer(t)

	// Create a run directory with no files inside it.
	run, err := state.CreateRun("test-dag")
	if err != nil {
		t.Fatal(err)
	}
	// Ensure it is truly empty.
	entries, err := os.ReadDir(run.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty run dir, got %d entries", len(entries))
	}

	req := httptest.NewRequest("POST", "/api/v1/dags/test-dag/runs/"+run.ID+"/archive", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST: %d %s", w.Code, w.Body.String())
	}
	var resp ArchiveResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Files != 0 {
		t.Errorf("files = %d, want 0", resp.Files)
	}

	// Archive still valid even with no files.
	report, err := intarchive.Verify(resp.Path)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !report.OK() {
		t.Errorf("verify not OK: %+v", report)
	}

	// GET the archive and confirm the manifest is still the first entry (just headers).
	getReq := httptest.NewRequest("GET", "/api/v1/dags/test-dag/runs/"+run.ID+"/archive", nil)
	getW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("GET: %d", getW.Code)
	}
	gz, err := gzip.NewReader(bytes.NewReader(getW.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("first entry: %v", err)
	}
	if hdr.Name != intarchive.ManifestName {
		t.Errorf("first entry = %q, want %q", hdr.Name, intarchive.ManifestName)
	}
	// EOF after the manifest since the run has no files.
	if _, err := tr.Next(); err != io.EOF {
		t.Errorf("expected EOF after manifest, got %v", err)
	}
}
