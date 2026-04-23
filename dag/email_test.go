package dag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeEmailDAG(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "e.yaml")
	yaml := "name: t\nsteps:\n  - id: e\n    email:\n" + body
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEmail_Valid(t *testing.T) {
	body := "      channel: team\n" +
		"      subject: Weekly report\n" +
		"      body: see attached\n"
	if _, err := ParseFile(writeEmailDAG(t, body)); err != nil {
		t.Fatalf("valid DAG rejected: %v", err)
	}
}

func TestEmail_MissingChannel(t *testing.T) {
	body := "      subject: Weekly\n      body: hi\n"
	_, err := ParseFile(writeEmailDAG(t, body))
	if err == nil || !strings.Contains(err.Error(), "email.channel") {
		t.Errorf("expected email.channel error, got %v", err)
	}
}

func TestEmail_MissingSubject(t *testing.T) {
	body := "      channel: team\n      body: hi\n"
	_, err := ParseFile(writeEmailDAG(t, body))
	if err == nil || !strings.Contains(err.Error(), "email.subject") {
		t.Errorf("expected email.subject error, got %v", err)
	}
}

func TestEmail_BothBodySources(t *testing.T) {
	body := "      channel: team\n      subject: s\n      body: hi\n      body_file: body.txt\n"
	_, err := ParseFile(writeEmailDAG(t, body))
	if err == nil {
		t.Fatal("expected error for both body + body_file set")
	}
}

func TestEmail_NoBody(t *testing.T) {
	body := "      channel: team\n      subject: s\n"
	_, err := ParseFile(writeEmailDAG(t, body))
	if err == nil {
		t.Fatal("expected error when no body source set")
	}
}

func TestEmail_StepType(t *testing.T) {
	body := "      channel: team\n      subject: s\n      body: hi\n"
	d, err := ParseFile(writeEmailDAG(t, body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := StepType(d.Steps[0]); got != "email" {
		t.Errorf("StepType = %q, want email", got)
	}
}
