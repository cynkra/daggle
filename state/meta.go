package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RunMeta holds reproducibility metadata for a DAG run.
type RunMeta struct {
	RunID        string            `json:"run_id"`
	DAGName      string            `json:"dag_name"`
	DAGHash      string            `json:"dag_hash,omitempty"`
	DAGPath      string            `json:"dag_path,omitempty"`
	StartTime    time.Time         `json:"start_time"`
	EndTime      time.Time         `json:"end_time,omitempty"`
	Status       string            `json:"status,omitempty"`
	RVersion     string            `json:"r_version,omitempty"`
	Platform     string            `json:"platform"`
	Params       map[string]string `json:"params,omitempty"`
	DaggleVersion string           `json:"daggle_version,omitempty"`
}

// WriteMeta writes the run metadata to meta.json in the given directory.
func WriteMeta(dir string, meta *RunMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "meta.json"), data, 0644)
}

// ReadMeta reads run metadata from meta.json in the given directory.
func ReadMeta(dir string) (*RunMeta, error) {
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil, err
	}
	var meta RunMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("unmarshal meta: %w", err)
	}
	return &meta, nil
}
