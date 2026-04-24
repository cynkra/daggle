package dag

import (
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
