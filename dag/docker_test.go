package dag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDockerDAG(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "d.yaml")
	yaml := "name: t\nsteps:\n  - id: d\n    docker:\n" + body
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDocker_Valid(t *testing.T) {
	body := "      image: alpine:3.20\n" +
		"      command: echo hi\n"
	if _, err := ParseFile(writeDockerDAG(t, body)); err != nil {
		t.Fatalf("valid DAG rejected: %v", err)
	}
}

func TestDocker_MissingImage(t *testing.T) {
	body := "      command: echo hi\n"
	_, err := ParseFile(writeDockerDAG(t, body))
	if err == nil || !strings.Contains(err.Error(), "docker.image") {
		t.Errorf("expected docker.image error, got %v", err)
	}
}

func TestDocker_NoCommandOrEntrypoint(t *testing.T) {
	body := "      image: alpine:3.20\n"
	_, err := ParseFile(writeDockerDAG(t, body))
	if err == nil {
		t.Fatal("expected error when neither command nor entrypoint is set")
	}
}

func TestDocker_InvalidPull(t *testing.T) {
	body := "      image: alpine:3.20\n" +
		"      command: echo hi\n" +
		"      pull: sometimes\n"
	_, err := ParseFile(writeDockerDAG(t, body))
	if err == nil {
		t.Fatal("expected error for invalid pull value")
	}
}

func TestDocker_InvalidVolume(t *testing.T) {
	body := "      image: alpine:3.20\n" +
		"      command: echo hi\n" +
		"      volumes:\n        - /mnt/data\n"
	_, err := ParseFile(writeDockerDAG(t, body))
	if err == nil {
		t.Fatal("expected error for volume without colon")
	}
}

func TestDocker_StepType(t *testing.T) {
	body := "      image: alpine:3.20\n      command: echo hi\n"
	d, err := ParseFile(writeDockerDAG(t, body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := StepType(d.Steps[0]); got != "docker" {
		t.Errorf("StepType = %q, want docker", got)
	}
}
