package state

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestWalkDAGFiles_FiltersAndSkipsBase(t *testing.T) {
	src := DAGSource{Name: "test", Dir: t.TempDir()}
	writeFile := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(src.Dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Valid candidates
	writeFile("a.yaml", "name: a")
	writeFile("b.yml", "name: b")
	// Must be skipped
	writeFile("base.yaml", "name: base")
	writeFile("base.yml", "name: base2")
	writeFile("README.md", "ignore me")
	writeFile("notes.txt", "ignore me")
	// Directories must be skipped
	if err := os.Mkdir(filepath.Join(src.Dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile("subdir/inner.yaml", "name: inner")

	var seen []string
	err := WalkDAGFiles([]DAGSource{src}, func(_ DAGSource, path string) error {
		seen = append(seen, filepath.Base(path))
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDAGFiles: %v", err)
	}
	sort.Strings(seen)
	want := []string{"a.yaml", "b.yml"}
	if len(seen) != len(want) {
		t.Fatalf("seen = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("seen[%d] = %q, want %q", i, seen[i], want[i])
		}
	}
}

func TestWalkDAGFiles_MissingSourceDirIsSilent(t *testing.T) {
	nonexistent := DAGSource{Name: "missing", Dir: filepath.Join(t.TempDir(), "does-not-exist")}
	calls := 0
	err := WalkDAGFiles([]DAGSource{nonexistent}, func(_ DAGSource, _ string) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error for missing source, got %v", err)
	}
	if calls != 0 {
		t.Errorf("callback invoked %d times, want 0", calls)
	}
}

func TestWalkDAGFiles_CallbackErrorShortCircuits(t *testing.T) {
	src := DAGSource{Name: "test", Dir: t.TempDir()}
	for _, name := range []string{"a.yaml", "b.yaml", "c.yaml"} {
		if err := os.WriteFile(filepath.Join(src.Dir, name), []byte("name: x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sentinel := errors.New("stop")
	calls := 0
	err := WalkDAGFiles([]DAGSource{src}, func(_ DAGSource, _ string) error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want sentinel", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (short-circuit on first error)", calls)
	}
}

func TestDAGNameFromFile(t *testing.T) {
	cases := map[string]string{
		"daily-etl.yaml":        "daily-etl",
		"daily-etl.yml":         "daily-etl",
		"/abs/path/my-dag.yaml": "my-dag",
		"weird.name.yaml":       "weird.name",
	}
	for in, want := range cases {
		if got := DAGNameFromFile(in); got != want {
			t.Errorf("DAGNameFromFile(%q) = %q, want %q", in, got, want)
		}
	}
}
