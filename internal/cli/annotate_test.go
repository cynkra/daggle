package cli

import (
	"strings"
	"testing"

	"github.com/cynkra/daggle/state"
)

func TestAnnotateCmd_WritesEvent(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", dataDir)

	run, err := state.CreateRun("demo")
	if err != nil {
		t.Fatal(err)
	}

	rootCmd.SetArgs([]string{"annotate", "demo", run.ID, "restarted manually", "--author", "alice"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("annotate returned error: %v", err)
	}

	events, err := state.ReadEvents(run.Dir)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	var found *state.Event
	for i := range events {
		if events[i].Type == state.EventRunAnnotated {
			found = &events[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no run_annotated event found; events=%+v", events)
	}
	if found.Note != "restarted manually" {
		t.Errorf("note = %q, want %q", found.Note, "restarted manually")
	}
	if found.Author != "alice" {
		t.Errorf("author = %q, want alice", found.Author)
	}
}

func TestAnnotateCmd_MissingRun(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", dataDir)

	rootCmd.SetArgs([]string{"annotate", "no-such-dag", "no-such-id", "note"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing run")
	}
	if !strings.Contains(err.Error(), "no-such-dag") && !strings.Contains(err.Error(), "run") {
		t.Errorf("error = %v, want a run-not-found style message", err)
	}
}

func TestAnnotateCmd_DefaultAuthor(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", dataDir)
	t.Setenv("USER", "sentinel-user")

	run, err := state.CreateRun("demo")
	if err != nil {
		t.Fatal(err)
	}

	// Reset author flag from any previous test run in the same process.
	annotateAuthor = ""

	rootCmd.SetArgs([]string{"annotate", "demo", run.ID, "note"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("annotate: %v", err)
	}

	events, _ := state.ReadEvents(run.Dir)
	var got string
	for _, e := range events {
		if e.Type == state.EventRunAnnotated {
			got = e.Author
		}
	}
	if got == "" {
		t.Fatal("no author recorded")
	}
	// On systems where os/user.Current() resolves, we prefer that. Otherwise
	// we expect the USER fallback. Either way, the author must be non-empty.
}
