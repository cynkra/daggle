package state

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds global daggle configuration from config.yaml.
type Config struct {
	Cleanup   *CleanupConfig    `yaml:"cleanup,omitempty"`
	Tools     map[string]string `yaml:"tools,omitempty"`
	Engine    EngineConfig      `yaml:"engine,omitempty"`
	Scheduler SchedulerConfig   `yaml:"scheduler,omitempty"`
}

// EngineConfig controls execution engine behavior.
type EngineConfig struct {
	GracePeriod      string `yaml:"grace_period,omitempty"`       // default "5s"
	ErrorContextLines int   `yaml:"error_context_lines,omitempty"` // default 50
}

// SchedulerConfig controls scheduler behavior.
type SchedulerConfig struct {
	PollInterval  string `yaml:"poll_interval,omitempty"`  // default "30s"
	MaxConcurrent int    `yaml:"max_concurrent,omitempty"` // default 4
	WatchDebounce string `yaml:"watch_debounce,omitempty"` // default "500ms"
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
