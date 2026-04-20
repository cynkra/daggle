package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cynkra/daggle/state"
)

// BenchmarkListDAGs exercises the list-DAGs handler against a directory
// of 50 DAG files. The second and subsequent requests should hit the
// parse cache and do only stats, not YAML parses.
func BenchmarkListDAGs(b *testing.B) {
	dagDir := b.TempDir()
	dataDir := b.TempDir()
	b.Setenv("DAGGLE_DATA_DIR", dataDir)

	for i := 0; i < 50; i++ {
		y := fmt.Sprintf(`name: dag-%d
owner: owner-%d
team: team-%d
tags: [etl, daily]
steps:
  - id: a
    command: echo a
  - id: b
    command: echo b
    depends: [a]
`, i, i%5, i%3)
		if err := os.WriteFile(filepath.Join(dagDir, fmt.Sprintf("dag-%d.yaml", i)), []byte(y), 0o644); err != nil {
			b.Fatal(err)
		}
	}

	srv := New(func() []state.DAGSource { return []state.DAGSource{{Name: "t", Dir: dagDir}} }, "bench")
	h := srv.Handler()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/api/v1/dags", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			b.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
}
