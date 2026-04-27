package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cynkra/daggle/state"
)

func TestWhyCmd_NoFailedRun(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", dataDir)

	rootCmd.SetArgs([]string{"why", "no-such-dag"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when no failed run exists")
	}
	if !strings.Contains(err.Error(), "no failed runs") {
		t.Errorf("error = %v, want contains 'no failed runs'", err)
	}
}

func TestWhyCmd_PicksLatestFailedRun(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", dataDir)

	run, err := state.CreateRun("demo")
	if err != nil {
		t.Fatal(err)
	}
	w := state.NewEventWriter(run.Dir)
	_ = w.Write(state.Event{Type: state.EventRunStarted})
	_ = w.Write(state.Event{Type: state.EventStepStarted, StepID: "build", Attempt: 1})
	_ = w.Write(state.Event{Type: state.EventStepFailed, StepID: "build", Error: "compile error", ErrorDetail: "undefined reference"})
	_ = w.Write(state.Event{Type: state.EventRunFailed})

	// Write a stderr log so the why command can tail it.
	stderr := filepath.Join(run.Dir, "build.stderr.log")
	if err := os.WriteFile(stderr, []byte("line1\nline2\nundefined reference to foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rootCmd.SetArgs([]string{"why", "demo"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("why returned error: %v", err)
	}
}

func TestWhyCmd_RedactsSecrets(t *testing.T) {
	dataDir := t.TempDir()
	dagDir := t.TempDir()
	t.Setenv("DAGGLE_DATA_DIR", dataDir)
	t.Setenv("DAGGLE_DAGS_DIR", dagDir)
	const secret = "supersecret-AAA-bbb"
	t.Setenv("MY_WHY_SECRET", secret)

	dagYAML := `name: redactdemo
env:
  TOK:
    value: "${env:MY_WHY_SECRET}"
    secret: true
steps:
  - id: build
    command: "echo hi"
`
	if err := os.WriteFile(filepath.Join(dagDir, "redactdemo.yaml"), []byte(dagYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	run, err := state.CreateRun("redactdemo")
	if err != nil {
		t.Fatal(err)
	}
	w := state.NewEventWriter(run.Dir)
	_ = w.Write(state.Event{Type: state.EventRunStarted})
	_ = w.Write(state.Event{Type: state.EventStepStarted, StepID: "build", Attempt: 1})
	// Pretend the redactor was missing at run time so the secret hit disk.
	_ = w.Write(state.Event{
		Type:        state.EventStepFailed,
		StepID:      "build",
		Error:       "auth failed: " + secret,
		ErrorDetail: "TOK=" + secret,
	})
	_ = w.Write(state.Event{Type: state.EventRunFailed})

	stderr := filepath.Join(run.Dir, "build.stderr.log")
	if err := os.WriteFile(stderr, []byte("Error: TOK rejected: "+secret+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureStdout(t, func() error {
		rootCmd.SetArgs([]string{"why", "redactdemo"})
		return rootCmd.Execute()
	})
	if err != nil {
		t.Fatalf("why returned error: %v\nstdout:\n%s", err, out)
	}
	if strings.Contains(out, secret) {
		t.Errorf("`daggle why` output leaked secret:\n%s", out)
	}
	if !strings.Contains(out, "***") {
		t.Errorf("expected *** masking in output:\n%s", out)
	}
}

func TestFirstFailedStep(t *testing.T) {
	summaries := []state.StepState{
		{StepID: "a", Status: "completed"},
		{StepID: "b", Status: "failed", Error: "boom"},
		{StepID: "c", Status: "failed", Error: "later"},
	}
	got := firstFailedStep(summaries)
	if got == nil || got.StepID != "b" {
		t.Errorf("firstFailedStep = %+v, want b", got)
	}
}
