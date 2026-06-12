package envutil

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func pathValue(t *testing.T, env []string) string {
	t.Helper()
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			return kv[len("PATH="):]
		}
	}
	t.Fatalf("no PATH entry in %v", env)
	return ""
}

func TestWithToolDirsOnPath(t *testing.T) {
	sep := string(os.PathListSeparator)

	tests := []struct {
		name     string
		env      []string
		dirs     []string
		wantPath string
		// wantNoPath asserts the result has no PATH entry at all.
		wantNoPath bool
	}{
		{
			name:     "prepends to existing PATH",
			env:      []string{"PATH=/usr/bin" + sep + "/bin"},
			dirs:     []string{"/usr/local/bin"},
			wantPath: "/usr/local/bin" + sep + "/usr/bin" + sep + "/bin",
		},
		{
			name:     "skips dirs already on PATH",
			env:      []string{"PATH=/usr/local/bin" + sep + "/usr/bin"},
			dirs:     []string{"/usr/local/bin"},
			wantPath: "/usr/local/bin" + sep + "/usr/bin",
		},
		{
			name:     "de-duplicates input dirs and preserves order",
			env:      []string{"PATH=/bin"},
			dirs:     []string{"/opt/a", "/opt/b", "/opt/a"},
			wantPath: "/opt/a" + sep + "/opt/b" + sep + "/bin",
		},
		{
			name:     "creates PATH when absent",
			env:      []string{"HOME=/home/x"},
			dirs:     []string{"/usr/local/bin"},
			wantPath: "/usr/local/bin",
		},
		{
			name:     "empty and duplicate dirs are dropped",
			env:      []string{"PATH=/bin"},
			dirs:     []string{"", "/opt/a", ""},
			wantPath: "/opt/a" + sep + "/bin",
		},
		{
			name:     "empty PATH value gets dirs only",
			env:      []string{"PATH="},
			dirs:     []string{"/opt/a"},
			wantPath: "/opt/a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WithToolDirsOnPath(tt.env, tt.dirs)
			if pathValue(t, got) != tt.wantPath {
				t.Errorf("PATH = %q, want %q", pathValue(t, got), tt.wantPath)
			}
		})
	}
}

func TestWithToolDirsOnPath_NoDirsReturnsInput(t *testing.T) {
	env := []string{"PATH=/usr/bin", "HOME=/home/x"}
	got := WithToolDirsOnPath(env, nil)
	if !slices.Equal(got, env) {
		t.Errorf("expected input returned unchanged, got %v", got)
	}
	// all-empty dirs also collapse to nothing
	got = WithToolDirsOnPath(env, []string{"", ""})
	if !slices.Equal(got, env) {
		t.Errorf("expected input returned unchanged for empty dirs, got %v", got)
	}
}

func TestWithToolDirsOnPath_PreservesOtherVars(t *testing.T) {
	env := []string{"HOME=/home/x", "PATH=/bin", "LANG=C"}
	got := WithToolDirsOnPath(env, []string{"/opt/a"})
	if !slices.Contains(got, "HOME=/home/x") || !slices.Contains(got, "LANG=C") {
		t.Errorf("non-PATH vars not preserved: %v", got)
	}
}
