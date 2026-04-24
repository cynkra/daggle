package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"

	"github.com/cynkra/daggle/cache"
	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
)

// tryCacheHit checks whether the step can be satisfied from cache.
// Returns (true, nil) if a cache hit was found and outputs were replayed.
func (e *Engine) tryCacheHit(step dag.Step, _ string, _ []string) (bool, error) {
	if !step.Cache || e.cacheStore == nil {
		return false, nil
	}
	cacheKey := e.computeCacheKey(step)
	entry, ok := e.cacheStore.Lookup(e.dag.Name, step.ID, cacheKey)
	if !ok {
		return false, nil
	}
	// Replay cached outputs
	e.collectOutputs(step.ID, entry.Outputs)
	e.writeEvent(state.Event{
		Type:   state.EventStepCached,
		StepID: step.ID,
		CacheInfo: &state.CacheInfo{
			CacheKey:    cacheKey,
			CachedRunID: entry.RunID,
		},
	})
	e.logger.Info("step cached", "step", step.ID, "cache_key", cacheKey[:12], "cached_run", entry.RunID)
	return true, nil
}

// computeCacheKey builds a deterministic cache key for a step based on its inputs.
func (e *Engine) computeCacheKey(step dag.Step) string {
	stepType := StepCacheType(step, e.dag)

	// Sorted env vars (DAG-level + step-level)
	var envVars []string
	for k, v := range e.dag.Env {
		envVars = append(envVars, k+"="+v.Value)
	}
	for k, v := range step.Env {
		envVars = append(envVars, k+"="+v.Value)
	}

	// Upstream outputs
	e.mu.Lock()
	outputs := make(map[string]string, len(e.outputs))
	for k, v := range e.outputs {
		outputs[k] = v
	}
	e.mu.Unlock()

	var renvLockHash string
	if e.meta != nil {
		renvLockHash = e.meta.RenvLockHash
	}

	return cache.ComputeStepKey(stepType, envVars, outputs, renvLockHash)
}

// StepCacheType returns the content identifier string used for cache key computation.
// It reads script file content when available.
func StepCacheType(step dag.Step, d *dag.DAG) string {
	switch {
	case step.Script != "":
		scriptPath := step.Script
		if !filepath.IsAbs(scriptPath) {
			workdir := d.ResolveWorkdir(step)
			scriptPath = filepath.Join(workdir, scriptPath)
		}
		data, err := os.ReadFile(scriptPath)
		if err == nil {
			return "script:" + string(data)
		}
		return "script:" + step.Script
	case step.RExpr != "":
		return "r_expr:" + step.RExpr
	case step.Command != "":
		return "command:" + step.Command
	default:
		return "type:" + dag.StepType(step)
	}
}

// fileHash computes the SHA-256 hash of a file.
func fileHash(path string) (string, error) {
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
