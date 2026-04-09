// Package cache implements step-level caching for daggle.
// Cache keys are SHA-256 hashes of: script content, env vars, upstream outputs,
// upstream artifact hashes, and renv.lock hash (if present).
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Entry stores the cached result of a step execution.
type Entry struct {
	RunID          string            `json:"run_id"`
	Outputs        map[string]string `json:"outputs,omitempty"`
	ArtifactHashes map[string]string `json:"artifact_hashes,omitempty"`
}

// Store manages the on-disk cache.
type Store struct {
	baseDir string
}

// NewStore creates a Store rooted at the given base directory.
func NewStore(baseDir string) *Store {
	return &Store{baseDir: baseDir}
}

// Lookup reads a cached entry for the given DAG, step, and cache key.
// Returns the entry and true on hit, or nil and false on miss.
func (s *Store) Lookup(dagName, stepID, cacheKey string) (*Entry, bool) {
	p := s.entryPath(dagName, stepID, cacheKey)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, false
	}
	return &entry, true
}

// Save writes a cache entry to disk.
func (s *Store) Save(dagName, stepID, cacheKey string, entry Entry) error {
	p := s.entryPath(dagName, stepID, cacheKey)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal cache entry: %w", err)
	}
	return os.WriteFile(p, data, 0644)
}

// Clear removes all cached entries for a specific DAG.
func (s *Store) Clear(dagName string) error {
	dir := filepath.Join(s.baseDir, dagName)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear cache for %q: %w", dagName, err)
	}
	return nil
}

// ClearAll removes the entire cache directory.
func (s *Store) ClearAll() error {
	if err := os.RemoveAll(s.baseDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear all cache: %w", err)
	}
	return nil
}

// ComputeKey returns a SHA-256 hex digest of the joined parts.
func ComputeKey(parts ...string) string {
	h := sha256.New()
	h.Write([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Store) entryPath(dagName, stepID, cacheKey string) string {
	return filepath.Join(s.baseDir, dagName, stepID, cacheKey+".json")
}
