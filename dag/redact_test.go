package dag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedactor(t *testing.T) {
	env := EnvMap{
		"PUBLIC":  {Value: "visible"},
		"SECRET1": {Value: "s3cr3t", Secret: true},
		"SECRET2": {Value: "p4ssw0rd", Secret: true},
	}
	r := NewRedactor(env)

	if !r.HasSecrets() {
		t.Error("should have secrets")
	}

	input := "Error: connection failed with password s3cr3t on host p4ssw0rd"
	redacted := r.Redact(input)
	if strings.Contains(redacted, "s3cr3t") || strings.Contains(redacted, "p4ssw0rd") {
		t.Errorf("redacted string still contains secrets: %q", redacted)
	}
	if !strings.Contains(redacted, "***") {
		t.Error("redacted string should contain ***")
	}

	// Nil redactor is safe
	var nilR *Redactor
	if nilR.Redact("test") != "test" {
		t.Error("nil redactor should pass through")
	}
}

func TestAllEnvMaps(t *testing.T) {
	d := &DAG{
		Env: EnvMap{"DAG_SECRET": {Value: "dag-val", Secret: true}},
		Steps: []Step{
			{ID: "a", Env: EnvMap{"STEP_A_SECRET": {Value: "step-a-val", Secret: true}}},
			{ID: "b"}, // no env — must not be added as an empty map
			{ID: "c", Env: EnvMap{"STEP_C": {Value: "step-c-val"}}},
		},
	}
	maps := AllEnvMaps(d)
	// Expect DAG env + step a env + step c env (3 total). Step b skipped.
	if got, want := len(maps), 3; got != want {
		t.Fatalf("len(AllEnvMaps) = %d, want %d", got, want)
	}
	r := NewRedactor(maps...)
	out := r.Redact("dag-val and step-a-val together")
	if strings.Contains(out, "dag-val") || strings.Contains(out, "step-a-val") {
		t.Errorf("AllEnvMaps redactor missed step or DAG secret: %q", out)
	}
}

func TestLoadRedactor(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MY_DB_PASSWORD", "supersecret")

	yaml := `name: redact-test
env:
  DB_URL:
    value: "postgres://user:${env:MY_DB_PASSWORD}@host/db"
    secret: true
steps:
  - id: a
    command: "echo hi"
    env:
      STEP_TOKEN:
        value: "${env:MY_DB_PASSWORD}"
        secret: true
`
	path := filepath.Join(dir, "redact-test.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	r, err := LoadRedactor(path)
	if err != nil {
		t.Fatalf("LoadRedactor: %v", err)
	}
	out := r.Redact("connection: postgres://user:supersecret@host/db; token=supersecret")
	if strings.Contains(out, "supersecret") {
		t.Errorf("LoadRedactor did not mask resolved secret: %q", out)
	}
}

func TestLoadRedactor_ResolutionFailureReturnsNoOp(t *testing.T) {
	dir := t.TempDir()
	yaml := `name: redact-fail
env:
  REQUIRED:
    value: "${env:DAGGLE_TEST_NONEXISTENT_VAR_THAT_SHOULD_NOT_BE_SET}"
    secret: true
steps:
  - id: a
    command: "echo hi"
`
	path := filepath.Join(dir, "fail.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	r, err := LoadRedactor(path)
	if err == nil {
		t.Fatal("expected error from unresolvable env ref")
	}
	if r == nil {
		t.Fatal("Redactor must be non-nil even on resolution failure")
	}
	if r.HasSecrets() {
		t.Error("no-op redactor should report no secrets")
	}
	// Pass-through on no-op redactor.
	if got := r.Redact("hello"); got != "hello" {
		t.Errorf("no-op redactor altered input: %q", got)
	}
}
