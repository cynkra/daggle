package state

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds global daggle configuration from config.yaml.
type Config struct {
	Cleanup       *CleanupConfig                 `yaml:"cleanup,omitempty"`
	Tools         map[string]string              `yaml:"tools,omitempty"`
	Engine        EngineConfig                   `yaml:"engine,omitempty"`
	Scheduler     SchedulerConfig                `yaml:"scheduler,omitempty"`
	Notifications map[string]NotificationChannel `yaml:"notifications,omitempty"`
}

// NotificationChannel describes a named notification target in config.yaml.
// Field usage depends on Type:
//   - "slack":   WebhookURL (required)
//   - "clickup": WebhookURL (required)
//   - "http":    WebhookURL (required), Method (optional, default POST), Headers (optional)
//   - "smtp":    SMTPHost, SMTPPort, SMTPFrom, SMTPTo (all required), SMTPUser/SMTPPassword (optional)
type NotificationChannel struct {
	Type         string            `yaml:"type"`
	WebhookURL   string            `yaml:"webhook_url,omitempty"`
	Method       string            `yaml:"method,omitempty"`
	Headers      map[string]string `yaml:"headers,omitempty"`
	SMTPHost     string            `yaml:"smtp_host,omitempty"`
	SMTPPort     int               `yaml:"smtp_port,omitempty"`
	SMTPFrom     string            `yaml:"smtp_from,omitempty"`
	SMTPTo       []string          `yaml:"smtp_to,omitempty"`
	SMTPUser     string            `yaml:"smtp_user,omitempty"`
	SMTPPassword string            `yaml:"smtp_password,omitempty"`
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
