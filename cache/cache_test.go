package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestComputeKey(t *testing.T) {
	k1 := ComputeKey("script.R", "ENV=foo", "renv-hash-abc")
	k2 := ComputeKey("script.R", "ENV=foo", "renv-hash-abc")
	k3 := ComputeKey("script.R", "ENV=bar", "renv-hash-abc")

	if k1 != k2 {
		t.Errorf("same inputs produced different keys: %s vs %s", k1, k2)
	}
	if k1 == k3 {
		t.Error("different inputs produced the same key")
	}
	if len(k1) != 64 {
		t.Errorf("expected 64-char hex digest, got %d chars", len(k1))
	}
}

func TestStore_SaveAndLookup(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	entry := Entry{
		RunID:          "run-123",
		Outputs:        map[string]string{"result": "42"},
		ArtifactHashes: map[string]string{"model": "abc123"},
	}

	if err := s.Save("my-dag", "step1", "key-aaa", entry); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok := s.Lookup("my-dag", "step1", "key-aaa")
	if !ok {
		t.Fatal("Lookup returned miss after Save")
	}
	if got.RunID != "run-123" {
		t.Errorf("RunID = %q, want %q", got.RunID, "run-123")
	}
	if got.Outputs["result"] != "42" {
		t.Errorf("Outputs[result] = %q, want %q", got.Outputs["result"], "42")
	}
	if got.ArtifactHashes["model"] != "abc123" {
		t.Errorf("ArtifactHashes[model] = %q, want %q", got.ArtifactHashes["model"], "abc123")
	}
}

func TestStore_LookupMiss(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	_, ok := s.Lookup("no-dag", "no-step", "no-key")
	if ok {
		t.Error("Lookup returned hit for nonexistent key")
	}
}

func TestStore_Clear(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	entry := Entry{RunID: "run-1"}
	_ = s.Save("dag-a", "step1", "k1", entry)
	_ = s.Save("dag-b", "step1", "k1", entry)

	if err := s.Clear("dag-a"); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	// dag-a should be gone
	if _, err := os.Stat(filepath.Join(dir, "dag-a")); !os.IsNotExist(err) {
		t.Error("expected dag-a cache directory to be removed")
	}

	// dag-b should still exist
	_, ok := s.Lookup("dag-b", "step1", "k1")
	if !ok {
		t.Error("Clear(dag-a) removed dag-b cache")
	}
}
