package archive

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Report summarizes a Verify call. The archive is sound iff
// len(Mismatched)+len(Missing)+len(Extra) == 0.
type Report struct {
	Files      int
	Mismatched []string
	Missing    []string
	Extra      []string
}

// OK reports whether the archive matched its manifest exactly.
func (r Report) OK() bool {
	return len(r.Mismatched) == 0 && len(r.Missing) == 0 && len(r.Extra) == 0
}

// Verify reads a .tar.gz produced by Create and recomputes hashes against the
// embedded manifest. The manifest must be the first entry; otherwise we treat
// the archive as malformed.
func Verify(archivePath string) (Report, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return Report{}, fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return Report{}, fmt.Errorf("gzip reader: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)

	hdr, err := tr.Next()
	if err != nil {
		return Report{}, fmt.Errorf("read first entry: %w", err)
	}
	if hdr.Name != ManifestName {
		return Report{}, fmt.Errorf("first entry is %q, want %q", hdr.Name, ManifestName)
	}
	manifestBytes, err := io.ReadAll(tr)
	if err != nil {
		return Report{}, fmt.Errorf("read manifest: %w", err)
	}

	expected, err := parseManifest(manifestBytes)
	if err != nil {
		return Report{}, err
	}

	report := Report{}
	seen := make(map[string]bool, len(expected))

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Report{}, fmt.Errorf("read entry: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		h := sha256.New()
		if _, err := io.Copy(h, tr); err != nil {
			return Report{}, fmt.Errorf("hash %s: %w", hdr.Name, err)
		}
		got := hex.EncodeToString(h.Sum(nil))
		want, ok := expected[hdr.Name]
		if !ok {
			report.Extra = append(report.Extra, hdr.Name)
			continue
		}
		seen[hdr.Name] = true
		report.Files++
		if got != want {
			report.Mismatched = append(report.Mismatched, hdr.Name)
		}
	}

	for name := range expected {
		if !seen[name] {
			report.Missing = append(report.Missing, name)
		}
	}

	sort.Strings(report.Mismatched)
	sort.Strings(report.Missing)
	sort.Strings(report.Extra)
	return report, nil
}

// parseManifest reads "<sha256>  <path>" lines into a path -> sha256 map.
func parseManifest(data []byte) (map[string]string, error) {
	out := make(map[string]string)
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	// Allow long lines (manifest paths may be deep).
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := stripCRLF(sc.Text())
		if line == "" {
			continue
		}
		// Format: 64-char hex, two spaces, path
		if len(line) < 66 || line[64] != ' ' || line[65] != ' ' {
			return nil, fmt.Errorf("malformed manifest line: %q", line)
		}
		sum := line[:64]
		path := line[66:]
		if _, err := hex.DecodeString(sum); err != nil {
			return nil, fmt.Errorf("manifest hash %q: %w", sum, err)
		}
		out[path] = sum
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan manifest: %w", err)
	}
	return out, nil
}
