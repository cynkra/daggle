package archive

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(abs), err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", abs, err)
		}
	}
}

func TestArchive_RoundTrip(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src, map[string]string{
		"meta.json":           `{"run_id":"abc"}`,
		"events.jsonl":        "line1\nline2\n",
		"logs/extract.stdout": "hello",
		"logs/extract.stderr": "",
	})

	out := filepath.Join(t.TempDir(), "run.tar.gz")
	res, err := Create(src, out)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.Files != 4 {
		t.Errorf("Files = %d, want 4", res.Files)
	}

	report, err := Verify(out)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !report.OK() {
		t.Errorf("verify failed: %+v", report)
	}
	if report.Files != 4 {
		t.Errorf("verified files = %d, want 4", report.Files)
	}
}

func TestVerify_DetectsTamperedFile(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src, map[string]string{
		"events.jsonl": "original",
		"meta.json":    "{}",
	})
	out := filepath.Join(t.TempDir(), "run.tar.gz")
	if _, err := Create(src, out); err != nil {
		t.Fatalf("Create: %v", err)
	}

	tampered := filepath.Join(t.TempDir(), "run-tampered.tar.gz")
	rebuildArchive(t, out, tampered, func(name string, body []byte) (string, []byte, bool) {
		if name == "events.jsonl" {
			return name, []byte("tampered!!"), true
		}
		return name, body, true
	})

	report, err := Verify(tampered)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.OK() {
		t.Errorf("expected verify to detect tampering, got OK report: %+v", report)
	}
	if !contains(report.Mismatched, "events.jsonl") {
		t.Errorf("mismatched should include events.jsonl, got %v", report.Mismatched)
	}
}

func TestVerify_DetectsMissingFile(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src, map[string]string{
		"a.txt": "a",
		"b.txt": "b",
	})
	out := filepath.Join(t.TempDir(), "run.tar.gz")
	if _, err := Create(src, out); err != nil {
		t.Fatalf("Create: %v", err)
	}

	stripped := filepath.Join(t.TempDir(), "run-stripped.tar.gz")
	rebuildArchive(t, out, stripped, func(name string, body []byte) (string, []byte, bool) {
		if name == "b.txt" {
			return "", nil, false
		}
		return name, body, true
	})

	report, err := Verify(stripped)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.OK() {
		t.Errorf("expected missing-file failure, got OK report: %+v", report)
	}
	if !contains(report.Missing, "b.txt") {
		t.Errorf("missing should include b.txt, got %v", report.Missing)
	}
}

func TestVerify_RejectsMissingManifest(t *testing.T) {
	out := filepath.Join(t.TempDir(), "no-manifest.tar.gz")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	if _, err := Verify(out); err == nil {
		t.Fatal("expected error for archive without manifest")
	}
}

func TestParseManifest_Malformed(t *testing.T) {
	cases := []string{
		"too short",
		strings.Repeat("z", 64) + "  path",
		strings.Repeat("a", 63) + "  path",
	}
	for _, in := range cases {
		if _, err := parseManifest([]byte(in)); err == nil {
			t.Errorf("expected error for %q", in)
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// rebuildArchive rewrites src to dst, applying transform to each file entry.
// The manifest entry is preserved untouched, allowing tests to simulate
// tampering or removal of payload files.
func rebuildArchive(t *testing.T, src, dst string, transform func(name string, body []byte) (newName string, newBody []byte, keep bool)) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	gzr, err := gzip.NewReader(in)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gzr.Close() }()
	tr := tar.NewReader(gzr)

	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	gzw := gzip.NewWriter(out)
	tw := tar.NewWriter(gzw)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("rebuild read: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("rebuild body: %v", err)
		}

		name := hdr.Name
		data := body
		keep := true
		if hdr.Name != ManifestName {
			name, data, keep = transform(hdr.Name, body)
		}
		if !keep {
			continue
		}
		newHdr := *hdr
		newHdr.Name = name
		newHdr.Size = int64(len(data))
		if err := tw.WriteHeader(&newHdr); err != nil {
			t.Fatalf("rebuild header: %v", err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("rebuild write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tw: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("close gzw: %v", err)
	}
}
