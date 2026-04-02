package renv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMajorMinor(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"4.4.1", "4.4"},
		{"4.4", "4.4"},
		{"4", "4"},
		{"", ""},
		{"3.6.3", "3.6"},
	}
	for _, tt := range tests {
		if got := MajorMinor(tt.input); got != tt.want {
			t.Errorf("MajorMinor(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDetect_NoLockfile(t *testing.T) {
	dir := t.TempDir()
	info := Detect(dir, "4.4.1", "aarch64-apple-darwin20")
	if info.Detected {
		t.Error("expected Detected=false when no renv.lock")
	}
}

func TestDetect_LockfileWithLibrary(t *testing.T) {
	dir := t.TempDir()

	// Create renv.lock
	if err := os.WriteFile(filepath.Join(dir, "renv.lock"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create library directory
	libDir := filepath.Join(dir, "renv", "library", "R-4.4", "aarch64-apple-darwin20")
	if err := os.MkdirAll(libDir, 0755); err != nil {
		t.Fatal(err)
	}

	info := Detect(dir, "4.4.1", "aarch64-apple-darwin20")
	if !info.Detected {
		t.Fatal("expected Detected=true")
	}
	if !info.LibraryReady {
		t.Error("expected LibraryReady=true")
	}
	if info.LibraryPath != libDir {
		t.Errorf("LibraryPath = %q, want %q", info.LibraryPath, libDir)
	}
	if info.LockfilePath != filepath.Join(dir, "renv.lock") {
		t.Errorf("LockfilePath = %q, want %q", info.LockfilePath, filepath.Join(dir, "renv.lock"))
	}
}

func TestDetect_LockfileWithoutLibrary(t *testing.T) {
	dir := t.TempDir()

	// Create renv.lock but no library directory
	if err := os.WriteFile(filepath.Join(dir, "renv.lock"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	info := Detect(dir, "4.4.1", "aarch64-apple-darwin20")
	if !info.Detected {
		t.Fatal("expected Detected=true")
	}
	if info.LibraryReady {
		t.Error("expected LibraryReady=false when library dir missing")
	}
}
