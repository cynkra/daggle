package state

import (
	"os"
	"path/filepath"
)

// ConfigDir returns the daggle config directory (XDG_CONFIG_HOME/daggle).
func ConfigDir() string {
	if dir := os.Getenv("DAGGLE_CONFIG_DIR"); dir != "" {
		return dir
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "daggle")
}

// DataDir returns the daggle data directory (XDG_DATA_HOME/daggle).
func DataDir() string {
	if dir := os.Getenv("DAGGLE_DATA_DIR"); dir != "" {
		return dir
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "daggle")
}

// DAGDir returns the directory where DAG YAML files are stored.
// Precedence: --dags-dir flag (DAGGLE_DAGS_DIR) > .daggle/ in cwd > ~/.config/daggle/dags
func DAGDir() string {
	// Explicit override via --dags-dir flag
	if dir := os.Getenv("DAGGLE_DAGS_DIR"); dir != "" {
		return dir
	}

	// If config dir is explicitly set, use its dags/ subdir
	if os.Getenv("DAGGLE_CONFIG_DIR") != "" {
		return filepath.Join(ConfigDir(), "dags")
	}

	// Check for project-local .daggle/ directory in cwd
	if cwd, err := os.Getwd(); err == nil {
		localDir := filepath.Join(cwd, ".daggle")
		if info, err := os.Stat(localDir); err == nil && info.IsDir() {
			return localDir
		}
	}

	// Fall back to global XDG location
	return filepath.Join(ConfigDir(), "dags")
}

// RunsDir returns the directory where run history is stored.
func RunsDir() string {
	return filepath.Join(DataDir(), "runs")
}
