package state

import (
	"fmt"
	"os"
	"path/filepath"
)

// DAGYAMLName is the per-run snapshot of the DAG YAML source. Saved by the
// CLI on run start so future runs can diff against it without depending on
// the upstream file (which may have been edited or moved).
const DAGYAMLName = "dag.yaml"

// DAGDiffName is the unified diff between the previous run's dag.yaml and
// this run's dag.yaml, written when DAGHash differs from the prior run.
const DAGDiffName = "dag_diff.patch"

// WriteDAGDiff writes a unified diff of the previous run's dag.yaml against
// the current run's dag.yaml into <currentRunDir>/dag_diff.patch when the
// two files differ. Returns true if a diff file was written.
//
// Behavior:
//   - Returns (false, nil) if priorRun is nil
//   - Returns (false, nil) if either dag.yaml snapshot is missing (the
//     previous run pre-dates Phase 9 and has no snapshot)
//   - Returns (false, nil) if the files are byte-identical
//   - Returns (true, nil) on success
func WriteDAGDiff(currentRunDir string, priorRun *RunInfo) (bool, error) {
	if priorRun == nil {
		return false, nil
	}
	priorPath := filepath.Join(priorRun.Dir, DAGYAMLName)
	currentPath := filepath.Join(currentRunDir, DAGYAMLName)

	priorBytes, err := os.ReadFile(priorPath)
	if err != nil {
		// Prior run has no snapshot — no diff possible.
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read prior dag.yaml: %w", err)
	}
	currentBytes, err := os.ReadFile(currentPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read current dag.yaml: %w", err)
	}

	priorLines := splitLines(string(priorBytes))
	currentLines := splitLines(string(currentBytes))

	labelA := fmt.Sprintf("%s/dag.yaml (run %s)", priorRun.DAGName, priorRun.ID)
	labelB := fmt.Sprintf("%s/dag.yaml (current)", priorRun.DAGName)
	patch := unifiedDiff(priorLines, currentLines, labelA, labelB)
	if patch == "" {
		return false, nil
	}

	out := filepath.Join(currentRunDir, DAGDiffName)
	if err := os.WriteFile(out, []byte(patch), 0o644); err != nil {
		return false, fmt.Errorf("write dag_diff.patch: %w", err)
	}
	return true, nil
}
