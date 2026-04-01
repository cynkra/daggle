package dag

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ParseFile reads and parses a YAML DAG definition from the given file path.
func ParseFile(path string) (*DAG, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open DAG file: %w", err)
	}
	defer f.Close()

	d, err := ParseReader(f)
	if err != nil {
		return nil, err
	}

	// Set SourceDir to the directory containing the DAG file
	absPath, err := filepath.Abs(path)
	if err == nil {
		d.SourceDir = filepath.Dir(absPath)
	}

	return d, nil
}

// ParseReader parses a YAML DAG definition from a reader.
func ParseReader(r io.Reader) (*DAG, error) {
	var d DAG
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}
	if err := Validate(&d); err != nil {
		return nil, err
	}
	return &d, nil
}
