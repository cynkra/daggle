package dag

import (
	"crypto/sha256"
	"encoding/hex"
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
	defer func() { _ = f.Close() }()

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

// HashFile computes the SHA-256 hash of a file's contents.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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

// BaseDefaults holds shared defaults from base.yaml.
// Only fields that make sense to share across DAGs are included.
type BaseDefaults struct {
	Env       EnvMap `yaml:"env,omitempty"`
	Workdir   string            `yaml:"workdir,omitempty"`
	Timeout   string            `yaml:"timeout,omitempty"`   // default step timeout
	Retry     *Retry            `yaml:"retry,omitempty"`     // default step retry
	OnSuccess *Hook             `yaml:"on_success,omitempty"`
	OnFailure *Hook             `yaml:"on_failure,omitempty"`
	OnExit    *Hook             `yaml:"on_exit,omitempty"`
}

// LoadBaseDefaults reads base.yaml from the given directory, if it exists.
func LoadBaseDefaults(dir string) (*BaseDefaults, error) {
	path := filepath.Join(dir, "base.yaml")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open base.yaml: %w", err)
	}
	defer func() { _ = f.Close() }()

	var b BaseDefaults
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("parse base.yaml: %w", err)
	}
	return &b, nil
}

// ApplyBaseDefaults merges base defaults into a DAG. DAG values win on conflict.
func ApplyBaseDefaults(d *DAG, b *BaseDefaults) {
	if b == nil {
		return
	}

	// Merge env: base values, then DAG values override
	if len(b.Env) > 0 {
		if d.Env == nil {
			d.Env = make(EnvMap)
		}
		for k, v := range b.Env {
			if _, exists := d.Env[k]; !exists {
				d.Env[k] = v
			}
		}
	}

	// Workdir: DAG wins
	if d.Workdir == "" && b.Workdir != "" {
		d.Workdir = b.Workdir
	}

	// Hooks: DAG wins
	if d.OnSuccess == nil && b.OnSuccess != nil {
		d.OnSuccess = b.OnSuccess
	}
	if d.OnFailure == nil && b.OnFailure != nil {
		d.OnFailure = b.OnFailure
	}
	if d.OnExit == nil && b.OnExit != nil {
		d.OnExit = b.OnExit
	}

	// Apply default timeout and retry to steps that don't have their own
	for i := range d.Steps {
		if d.Steps[i].Timeout == "" && b.Timeout != "" {
			d.Steps[i].Timeout = b.Timeout
		}
		if d.Steps[i].Retry == nil && b.Retry != nil {
			d.Steps[i].Retry = b.Retry
		}
	}
}
