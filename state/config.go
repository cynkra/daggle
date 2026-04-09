package state

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds global daggle configuration from config.yaml.
type Config struct {
	Cleanup *CleanupConfig `yaml:"cleanup,omitempty"`
}

// CleanupConfig controls automatic run cleanup in the scheduler.
type CleanupConfig struct {
	OlderThan string `yaml:"older_than"` // e.g. "30d", "24h"
	Interval  string `yaml:"interval"`   // how often to run cleanup, e.g. "1h", "6h" (default: "1h")
}

// ConfigPath returns the path to the global config file.
func ConfigPath() string {
	return filepath.Join(ConfigDir(), "config.yaml")
}

// LoadConfig reads the global config. Returns zero-value Config if file doesn't exist.
func LoadConfig() (Config, error) {
	var cfg Config
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
