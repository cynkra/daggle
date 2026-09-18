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
	Server        ServerConfig                   `yaml:"server,omitempty"`
	Notifications map[string]NotificationChannel `yaml:"notifications,omitempty"`
}

// ServerConfig controls how `daggle serve` exposes its HTTP API and UI.
//
// Every field is also settable by flag or environment variable (see
// internal/cli/serve.go), but the file is the primary surface: deployments
// that generate their configuration from templates need to express the whole
// server posture in one rendered file, with no interactive setup step.
type ServerConfig struct {
	// Bind is the address the API listens on. Default "127.0.0.1".
	// Set to "0.0.0.0" to accept connections from other containers or hosts;
	// doing so requires an auth mode other than "none".
	Bind string `yaml:"bind,omitempty"`
	// Port is the API port. A --port flag overrides it; 0 means the API is
	// not started at all.
	Port int `yaml:"port,omitempty"`
	// BasePath mounts the API and UI under a sub-path, e.g. "/daggle", for
	// deployments behind a reverse proxy that does not strip the prefix.
	BasePath string `yaml:"base_path,omitempty"`
	// TrustProxy makes daggle honour X-Forwarded-Proto/Host/For. Enable it
	// only when daggle is genuinely behind a proxy that sets them, since a
	// direct client can otherwise forge its own apparent scheme and address.
	TrustProxy bool `yaml:"trust_proxy,omitempty"`
	// Auth configures who may call the API.
	Auth AuthConfig `yaml:"auth,omitempty"`
}

// AuthConfig selects the single-tenant authentication mode and its credentials.
//
// The *File variants read the secret from a file at startup, which is how
// credentials reach a container that decrypts them from an encrypted store at
// entrypoint time. When both are set, the inline value wins.
type AuthConfig struct {
	// Mode is "none", "basic" or "token". Default "none", which is only
	// permitted on a loopback bind.
	Mode string `yaml:"mode,omitempty"`
	// Username and Password are used by mode "basic".
	Username     string `yaml:"username,omitempty"`
	Password     string `yaml:"password,omitempty"`
	PasswordFile string `yaml:"password_file,omitempty"`
	// Token is the shared bearer token used by mode "token". When mode is
	// "token" and no token is configured, daggle generates one on first start
	// and persists it under the data directory.
	Token     string `yaml:"token,omitempty"`
	TokenFile string `yaml:"token_file,omitempty"`
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
	GracePeriod       string `yaml:"grace_period,omitempty"`        // default "5s"
	ErrorContextLines int    `yaml:"error_context_lines,omitempty"` // default 50
}

// SchedulerConfig controls scheduler behavior.
type SchedulerConfig struct {
	PollInterval   string `yaml:"poll_interval,omitempty"`    // default "30s"
	MaxConcurrent  int    `yaml:"max_concurrent,omitempty"`   // default 4
	WatchDebounce  string `yaml:"watch_debounce,omitempty"`   // default "500ms"
	MaxCatchupRuns int    `yaml:"max_catchup_runs,omitempty"` // default 100; cap on catchup: all
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
