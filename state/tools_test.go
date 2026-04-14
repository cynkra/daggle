package state

import (
	"os/exec"
	"sync"
	"testing"
)

// resetTools resets the sync.Once and resolved map for testing.
func resetTools() {
	toolsOnce = sync.Once{}
	resolvedTools = nil
}

func TestInitTools_ConfigOverride(t *testing.T) {
	resetTools()
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
	// Initialize with empty config; tools not on PATH will fall back to bare name
	// We test this by checking a tool that isn't on PATH won't crash
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
