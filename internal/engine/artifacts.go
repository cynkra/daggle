package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
)

// verifyArtifacts checks that declared artifacts exist, computes hashes, and writes events.
func (e *Engine) verifyArtifacts(step dag.Step, workdir string) error {
	if len(step.Artifacts) == 0 {
		return nil
	}
	for _, art := range step.Artifacts {
		absPath := art.Path
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(workdir, art.Path)
		}

		// Handle versioned artifacts: rename file to include epoch timestamp
		if art.Versioned {
			ext := filepath.Ext(absPath)
			base := strings.TrimSuffix(absPath, ext)
			versionedPath := fmt.Sprintf("%s_%d%s", base, time.Now().Unix(), ext)
			if err := os.Rename(absPath, versionedPath); err != nil {
				return fmt.Errorf("step %q artifact %q: failed to version file: %w", step.ID, art.Name, err)
			}
			absPath = versionedPath
		}

		info, err := os.Stat(absPath)
		if err != nil {
			return fmt.Errorf("step %q artifact %q not found at %s", step.ID, art.Name, absPath)
		}

		hash, err := fileHash(absPath)
		if err != nil {
			return fmt.Errorf("step %q artifact %q: hash failed: %w", step.ID, art.Name, err)
		}

		// Recompute relative path (may have changed if versioned)
		relPath := art.Path
		if art.Versioned {
			relPath, _ = filepath.Rel(workdir, absPath)
		}

		e.writeEvent(state.Event{
			Type:   state.EventStepArtifact,
			StepID: step.ID,
			ArtifactInfo: &state.ArtifactInfo{
				ArtifactName:    art.Name,
				ArtifactPath:    relPath,
				ArtifactAbsPath: absPath,
				ArtifactHash:    hash,
				ArtifactSize:    info.Size(),
				ArtifactFormat:  art.Format,
			},
		})
		e.logger.Info("artifact recorded", "step", step.ID, "artifact", art.Name, "path", absPath, "size", info.Size())
	}
	return nil
}
