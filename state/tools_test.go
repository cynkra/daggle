package state

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// resetTools resets the sync.Once and resolved map for testing.
func resetTools() {
	toolsOnce = sync.Once{}
	resolvedTools = nil
}

// withTempConfig sets DAGGLE_CONFIG_DIR to a temp directory for the duration of the test,
// so that saveToolPaths writes to an isolated location.
func withTempConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DAGGLE_CONFIG_DIR", dir)
	return dir
}

func TestInitTools_ConfigOverride(t *testing.T) {
	resetTools()
	withTempConfig(t)
	cfg := Config{
		Tools: map[string]string{
			"rscript": "/custom/bin/Rscript",
			"quarto":  "/custom/bin/quarto",
		},
	}
	InitTools(cfg)

	if got := ToolPath("rscript"); got != "/custom/bin/Rscript" {
		t.Errorf("ToolPath(rscript) = %q, want /custom/bin/Rscript", got)
	}
	if got := ToolPath("quarto"); got != "/custom/bin/quarto" {
		t.Errorf("ToolPath(quarto) = %q, want /custom/bin/quarto", got)
	}
}

func TestInitTools_LookPath(t *testing.T) {
	resetTools()
	withTempConfig(t)
	// sh should always be found via LookPath
	InitTools(Config{})

	got := ToolPath("sh")
	expected, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not found on this system")
	}
	if got != expected {
		t.Errorf("ToolPath(sh) = %q, want %q", got, expected)
	}
}

func TestInitTools_FallbackToBare(t *testing.T) {
	resetTools()
	withTempConfig(t)
	// Initialize with empty config; tools not on PATH will fall back to bare name
	InitTools(Config{})

	// All known tools should return something (either resolved or bare)
	for key, bin := range knownTools {
		got := ToolPath(key)
		if got == "" {
			t.Errorf("ToolPath(%q) returned empty string, want at least %q", key, bin)
		}
	}
}

func TestToolPath_UnknownTool(t *testing.T) {
	resetTools()
	withTempConfig(t)
	InitTools(Config{})

	got := ToolPath("nonexistent-tool")
	if got != "nonexistent-tool" {
		t.Errorf("ToolPath(nonexistent-tool) = %q, want %q", got, "nonexistent-tool")
	}
}

func TestToolPath_BeforeInit(t *testing.T) {
	resetTools()
	// Before InitTools, should fall back to known binary names
	if got := ToolPath("rscript"); got != "Rscript" {
		t.Errorf("ToolPath(rscript) before init = %q, want Rscript", got)
	}
	if got := ToolPath("unknown"); got != "unknown" {
		t.Errorf("ToolPath(unknown) before init = %q, want unknown", got)
	}
}

func TestResolvedTools_ReturnsCopy(t *testing.T) {
	resetTools()
	withTempConfig(t)
	InitTools(Config{})

	tools1 := ResolvedTools()
	tools1["rscript"] = "mutated"

	tools2 := ResolvedTools()
	if tools2["rscript"] == "mutated" {
		t.Error("ResolvedTools() returned reference, not copy")
	}
}

func TestInitTools_OnlyRunsOnce(t *testing.T) {
	resetTools()
	withTempConfig(t)
	InitTools(Config{
		Tools: map[string]string{"rscript": "/first/Rscript"},
	})
	// Second call with different config should be ignored
	InitTools(Config{
		Tools: map[string]string{"rscript": "/second/Rscript"},
	})

	if got := ToolPath("rscript"); got != "/first/Rscript" {
		t.Errorf("ToolPath(rscript) = %q, want /first/Rscript (sync.Once should prevent second init)", got)
	}
}

func TestInitTools_PersistsDiscoveredPaths(t *testing.T) {
	resetTools()
	configDir := withTempConfig(t)

	// Init with no config — LookPath should discover sh and persist it
	InitTools(Config{})

	// Read the persisted config
	data, err := os.ReadFile(filepath.Join(configDir, "config.yaml"))
	if err != nil {
		t.Fatalf("config.yaml not written: %v", err)
	}
	content := string(data)

	// sh should always be discoverable and persisted
	shPath, lookErr := exec.LookPath("sh")
	if lookErr != nil {
		t.Skip("sh not found on this system")
	}
	if got := ToolPath("sh"); got != shPath {
		t.Errorf("ToolPath(sh) = %q, want %q", got, shPath)
	}
	if !contains(content, shPath) {
		t.Errorf("config.yaml does not contain persisted sh path %q:\n%s", shPath, content)
	}
}

func TestInitTools_SkipsPersistWhenAllConfigured(t *testing.T) {
	resetTools()
	configDir := withTempConfig(t)

	// Provide all tools via config — nothing to discover
	cfg := Config{
		Tools: map[string]string{
			"rscript": "/custom/Rscript",
			"quarto":  "/custom/quarto",
			"git":     "/custom/git",
			"sh":      "/custom/sh",
			"docker":  "/custom/docker",
		},
	}
	InitTools(cfg)

	// config.yaml should still be written with the persisted paths
	configPath := filepath.Join(configDir, "config.yaml")
	if _, err := os.Stat(configPath); err == nil {
		// If file exists, that's unexpected since nothing was discovered via LookPath
		t.Error("config.yaml was written even though all tools were already configured")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && len(substr) > 0 && containsStr(s, substr)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
