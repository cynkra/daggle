package state

import (
	"os/exec"
	"sync"
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
// Call this once at startup after loading config.
func InitTools(cfg Config) {
	toolsOnce.Do(func() {
		resolvedTools = make(map[string]string, len(knownTools))
		for key, bin := range knownTools {
			// Check user config first
			if p, ok := cfg.Tools[key]; ok && p != "" {
				resolvedTools[key] = p
				continue
			}
			// Try to resolve via PATH
			if p, err := exec.LookPath(bin); err == nil {
				resolvedTools[key] = p
				continue
			}
			// Fall back to bare name
			resolvedTools[key] = bin
		}
	})
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
