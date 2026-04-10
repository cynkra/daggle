package dag

import (
	"fmt"
	"path/filepath"
)

// LoadAndExpand reads a DAG file, applies base.yaml defaults, and expands
// templates with the given parameters. It does not resolve secret references
// in env vars — callers that need secrets should call ResolveEnv separately.
func LoadAndExpand(path string, params map[string]string) (*DAG, error) {
	d, err := ParseFile(path)
	if err != nil {
		return nil, fmt.Errorf("parse DAG: %w", err)
	}

	baseDefaults, err := LoadBaseDefaults(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("load base.yaml: %w", err)
	}
	ApplyBaseDefaults(d, baseDefaults)

	expanded, err := ExpandDAG(d, params)
	if err != nil {
		return nil, fmt.Errorf("expand templates: %w", err)
	}

	return expanded, nil
}
