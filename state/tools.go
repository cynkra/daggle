package state

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// knownTools maps canonical lowercase keys to actual binary names.
var knownTools = map[string]string{
	"rscript": "Rscript",
	"quarto":  "quarto",
	"git":     "git",
	"sh":      "sh",
}

var (
	toolsOnce     sync.Once
	resolvedTools map[string]string
)

// InitTools resolves absolute paths for known external tools.
// For each tool it checks: (1) user-configured path from config.yaml,
// (2) exec.LookPath to find it on the current PATH, (3) bare binary name as fallback.
// Any paths discovered via LookPath are persisted to config.yaml so that
// future runs (e.g. scheduler as a system service) find them without a full PATH.
// Call this once at startup after loading config.
func InitTools(cfg Config) {
	toolsOnce.Do(func() {
		resolvedTools = make(map[string]string, len(knownTools))
		var discovered map[string]string // paths found via LookPath (not yet in config)
		for key, bin := range knownTools {
			// Check user config first
			if p, ok := cfg.Tools[key]; ok && p != "" {
				resolvedTools[key] = p
				continue
			}
			// Try to resolve via PATH
			if p, err := exec.LookPath(bin); err == nil {
				resolvedTools[key] = p
				if discovered == nil {
					discovered = make(map[string]string)
				}
				discovered[key] = p
				continue
			}
			// Fall back to bare name
			resolvedTools[key] = bin
		}
		// Persist newly discovered paths to config.yaml
		if len(discovered) > 0 {
			if err := saveToolPaths(cfg, discovered); err != nil {
				slog.Warn("failed to persist tool paths", "error", err)
			}
		}
	})
}

// saveToolPaths merges discovered tool paths into the existing config.yaml.
func saveToolPaths(cfg Config, discovered map[string]string) error {
	if cfg.Tools == nil {
		cfg.Tools = make(map[string]string, len(discovered))
	}
	for key, path := range discovered {
		cfg.Tools[key] = path
	}

	configPath := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0o644)
}

// ToolPath returns the resolved absolute path for a tool.
// The name should be a lowercase canonical key (e.g. "rscript", "quarto").
// If InitTools has not been called, returns the bare binary name as a fallback.
func ToolPath(name string) string {
	if resolvedTools != nil {
		if p, ok := resolvedTools[name]; ok {
			return p
		}
	}
	// Fallback: return the known binary name, or the key itself
	if bin, ok := knownTools[name]; ok {
		return bin
	}
	return name
}

// ResolvedTools returns a copy of the resolved tool paths map.
func ResolvedTools() map[string]string {
	result := make(map[string]string, len(knownTools))
	for key, bin := range knownTools {
		if resolvedTools != nil {
			if p, ok := resolvedTools[key]; ok {
				result[key] = p
				continue
			}
		}
		result[key] = bin
	}
	return result
}
