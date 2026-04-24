package dag

import (
	"strings"
	"testing"
)

func TestEnvVar_UnmarshalYAML(t *testing.T) {
	// Plain string form
	yamlStr := `name: test
env:
  PLAIN: literal
  WITH_REF: "${env:FOO}"
steps:
  - id: a
    command: echo
`
	d, err := ParseReader(strings.NewReader(yamlStr))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.Env["PLAIN"].Value != "literal" {
		t.Errorf("PLAIN = %q, want %q", d.Env["PLAIN"].Value, "literal")
	}
	if d.Env["PLAIN"].Secret {
		t.Error("PLAIN should not be secret")
	}
	if d.Env["WITH_REF"].Value != "${env:FOO}" {
		t.Errorf("WITH_REF = %q", d.Env["WITH_REF"].Value)
	}
}

func TestEnvVar_UnmarshalYAML_MapForm(t *testing.T) {
	yamlStr := `name: test
env:
  DB_PASS:
    value: "${env:DATABASE_PASSWORD}"
    secret: true
  PLAIN:
    value: "literal"
steps:
  - id: a
    command: echo
`
	d, err := ParseReader(strings.NewReader(yamlStr))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !d.Env["DB_PASS"].Secret {
		t.Error("DB_PASS should be secret")
	}
	if d.Env["DB_PASS"].Value != "${env:DATABASE_PASSWORD}" {
		t.Errorf("DB_PASS value = %q", d.Env["DB_PASS"].Value)
	}
	if d.Env["PLAIN"].Secret {
		t.Error("PLAIN should not be secret")
	}
}
