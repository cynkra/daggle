package state

import (
	"os"
	"path/filepath"
)

// ConfigDir returns the rdag config directory (XDG_CONFIG_HOME/rdag).
func ConfigDir() string {
	if dir := os.Getenv("RDAG_CONFIG_DIR"); dir != "" {
		return dir
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "rdag")
}

// DataDir returns the rdag data directory (XDG_DATA_HOME/rdag).
func DataDir() string {
	if dir := os.Getenv("RDAG_DATA_DIR"); dir != "" {
		return dir
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "rdag")
}

// DAGDir returns the directory where DAG YAML files are stored.
func DAGDir() string {
	return filepath.Join(ConfigDir(), "dags")
}

// RunsDir returns the directory where run history is stored.
func RunsDir() string {
	return filepath.Join(DataDir(), "runs")
}
