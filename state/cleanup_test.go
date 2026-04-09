package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseDurationWithDays(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"30d", 30 * 24 * time.Hour},
		{"1d", 24 * time.Hour},
		{"24h", 24 * time.Hour},
		{"1h30m", 90 * time.Minute},
	}
	for _, tt := range tests {
		got, err := ParseDurationWithDays(tt.input)
		if err != nil {
			t.Errorf("ParseDurationWithDays(%q): %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseDurationWithDays(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestCleanupRuns(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", tmpDir)

	// Create an old run
	oldRunDir := filepath.Join(RunsDir(), "test-dag", "2024-01-01", "run_abc123")
	if err := os.MkdirAll(oldRunDir, 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(oldRunDir, "events.jsonl"), []byte("test"), 0644)

	// Set the mod time to 60 days ago
	oldTime := time.Now().Add(-60 * 24 * time.Hour)
	_ = os.Chtimes(oldRunDir, oldTime, oldTime)

	// Create a recent run
	recentRunDir := filepath.Join(RunsDir(), "test-dag", "2024-06-01", "run_def456")
	if err := os.MkdirAll(recentRunDir, 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(recentRunDir, "events.jsonl"), []byte("test"), 0644)

	// Cleanup runs older than 30 days
	result, err := CleanupRuns(30 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("CleanupRuns: %v", err)
	}

	if result.Removed != 1 {
		t.Errorf("removed = %d, want 1", result.Removed)
	}

	// Old run should be gone
	if _, err := os.Stat(oldRunDir); !os.IsNotExist(err) {
		t.Error("old run directory should be removed")
	}

	// Recent run should still exist
	if _, err := os.Stat(recentRunDir); err != nil {
		t.Error("recent run directory should still exist")
	}
}

func TestLoadConfig_Missing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DAGGLE_CONFIG_DIR", tmpDir)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Cleanup != nil {
		t.Error("expected nil cleanup config when file is missing")
	}
}

func TestLoadConfig_WithCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DAGGLE_CONFIG_DIR", tmpDir)

	_ = os.WriteFile(filepath.Join(tmpDir, "config.yaml"), []byte("cleanup:\n  older_than: 30d\n  interval: 6h\n"), 0644)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Cleanup == nil {
		t.Fatal("expected cleanup config")
	}
	if cfg.Cleanup.OlderThan != "30d" {
		t.Errorf("older_than = %q, want 30d", cfg.Cleanup.OlderThan)
	}
	if cfg.Cleanup.Interval != "6h" {
		t.Errorf("interval = %q, want 6h", cfg.Cleanup.Interval)
	}
}
