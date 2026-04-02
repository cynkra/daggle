package renv

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Info holds detected renv configuration.
type Info struct {
	LockfilePath string // absolute path to renv.lock
	LibraryPath  string // resolved renv/library/R-<major.minor>/<platform>/
	Detected     bool   // true if renv.lock was found
	LibraryReady bool   // true if the library directory actually exists
}

// Detect checks for renv.lock in projectDir and resolves the library path.
// rVersion is the full version string (e.g. "4.4.1"); rPlatform is R's platform
// string (e.g. "aarch64-apple-darwin20"). Returns Info with Detected=false if
// no renv.lock is found.
func Detect(projectDir, rVersion, rPlatform string) Info {
	lockfile := filepath.Join(projectDir, "renv.lock")
	if _, err := os.Stat(lockfile); err != nil {
		return Info{}
	}

	mm := MajorMinor(rVersion)
	libPath := filepath.Join(projectDir, "renv", "library", "R-"+mm, rPlatform)

	info := Info{
		LockfilePath: lockfile,
		LibraryPath:  libPath,
		Detected:     true,
	}

	if fi, err := os.Stat(libPath); err == nil && fi.IsDir() {
		info.LibraryReady = true
	}

	return info
}

// DetectRPlatform runs Rscript to get R.version$platform. Returns "" on error.
func DetectRPlatform() string {
	out, err := exec.Command("Rscript", "-e", "cat(R.version$platform)").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(bytes.TrimRight(out, "\n")))
}

// MajorMinor extracts "4.4" from "4.4.1".
func MajorMinor(version string) string {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return version
	}
	return parts[0] + "." + parts[1]
}
