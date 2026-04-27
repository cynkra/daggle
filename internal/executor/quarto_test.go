package executor

import (
	"strings"
	"testing"
)

func TestResolveQuartoOutputDir(t *testing.T) {
	tests := []struct {
		name      string
		outputDir string
		workdir   string
		wantErr   string
	}{
		{
			name:      "relative under workdir",
			outputDir: "out",
			workdir:   "/work",
		},
		{
			name:      "nested under workdir",
			outputDir: "build/site",
			workdir:   "/work",
		},
		{
			name:      "self workdir",
			outputDir: ".",
			workdir:   "/work",
		},
		{
			name:      "absolute path is allowed",
			outputDir: "/tmp/site",
			workdir:   "/work",
		},
		{
			name:      "traversal rejected",
			outputDir: "../escape",
			workdir:   "/work",
			wantErr:   "escapes workdir",
		},
		{
			name:      "deep traversal rejected",
			outputDir: "../../../etc",
			workdir:   "/work/deep",
			wantErr:   "escapes workdir",
		},
		{
			name:      "empty workdir with relative dir is rejected",
			outputDir: "out",
			workdir:   "",
			wantErr:   "workdir is empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveQuartoOutputDir(tt.outputDir, tt.workdir)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected ok, got error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want contains %q", err, tt.wantErr)
			}
		})
	}
}
