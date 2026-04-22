// Package archive bundles a run directory into a tamper-evident tar.gz with
// an in-archive SHA-256 manifest, and verifies bundles created the same way.
//
// Archive layout: a single .tar.gz containing
//
//	.manifest.sha256   first entry, lines of "<hex-sha256>  <relative-path>"
//	<run files...>     all regular files from the source directory, sorted
//
// Verify recomputes every file's hash and compares to the manifest, reporting
// mismatches, missing files, and extras.
package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ManifestName is the path of the manifest entry inside the archive.
const ManifestName = ".manifest.sha256"

// Result summarizes a successful Create call.
type Result struct {
	OutPath string
	Files   int
	Bytes   int64
}

// Create bundles every regular file under srcDir into outPath as a gzipped
// tar archive with a leading SHA-256 manifest. Output is deterministic: files
// are written in sorted relative-path order, and the manifest is sorted to
// match.
func Create(srcDir, outPath string) (Result, error) {
	srcDir = filepath.Clean(srcDir)
	info, err := os.Stat(srcDir)
	if err != nil {
		return Result{}, fmt.Errorf("stat source: %w", err)
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("source %q is not a directory", srcDir)
	}

	files, err := collectFiles(srcDir)
	if err != nil {
		return Result{}, err
	}

	hashes := make([]fileHash, 0, len(files))
	var totalBytes int64
	for _, rel := range files {
		abs := filepath.Join(srcDir, rel)
		sum, n, err := hashFile(abs)
		if err != nil {
			return Result{}, fmt.Errorf("hash %s: %w", rel, err)
		}
		hashes = append(hashes, fileHash{Rel: rel, Sum: sum})
		totalBytes += n
	}

	manifest := buildManifest(hashes)

	out, err := os.Create(outPath)
	if err != nil {
		return Result{}, fmt.Errorf("create archive: %w", err)
	}
	defer func() { _ = out.Close() }()

	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)

	if err := writeTarEntry(tw, ManifestName, []byte(manifest), 0o644, info.ModTime()); err != nil {
		return Result{}, err
	}

	for _, rel := range files {
		abs := filepath.Join(srcDir, rel)
		if err := addFileToTar(tw, abs, rel); err != nil {
			return Result{}, err
		}
	}

	if err := tw.Close(); err != nil {
		return Result{}, fmt.Errorf("close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return Result{}, fmt.Errorf("close gzip: %w", err)
	}
	if err := out.Close(); err != nil {
		return Result{}, fmt.Errorf("close archive: %w", err)
	}

	return Result{OutPath: outPath, Files: len(files), Bytes: totalBytes}, nil
}

type fileHash struct {
	Rel string
	Sum string
}

func collectFiles(srcDir string) ([]string, error) {
	var files []string
	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		// Normalize to forward slashes for portability inside the tarball.
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk source: %w", err)
	}
	sort.Strings(files)
	return files, nil
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func buildManifest(hashes []fileHash) string {
	var b bytes.Buffer
	for _, h := range hashes {
		fmt.Fprintf(&b, "%s  %s\n", h.Sum, h.Rel)
	}
	return b.String()
}

func writeTarEntry(tw *tar.Writer, name string, data []byte, mode int64, mtime time.Time) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    mode,
		Size:    int64(len(data)),
		ModTime: mtime,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("tar header %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("tar body %s: %w", name, err)
	}
	return nil
}

func addFileToTar(tw *tar.Writer, abs, rel string) error {
	f, err := os.Open(abs)
	if err != nil {
		return fmt.Errorf("open %s: %w", rel, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", rel, err)
	}
	hdr := &tar.Header{
		Name:    rel,
		Mode:    int64(info.Mode().Perm()),
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("tar header %s: %w", rel, err)
	}
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("tar body %s: %w", rel, err)
	}
	return nil
}

// stripCRLF guards against accidental newline normalization confusion when
// callers parse manifest lines on Windows-edited inputs.
func stripCRLF(s string) string {
	return strings.TrimRight(s, "\r\n")
}
