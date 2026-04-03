package dag

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveEnv_Literal(t *testing.T) {
	env := EnvMap{
		"PLAIN": {Value: "literal_value"},
	}
	if err := ResolveEnv(env); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env["PLAIN"].Value != "literal_value" {
		t.Errorf("PLAIN = %q, want %q", env["PLAIN"].Value, "literal_value")
	}
	if env["PLAIN"].Secret {
		t.Error("PLAIN should not be secret")
	}
}

func TestResolveEnv_EnvRef(t *testing.T) {
	t.Setenv("TEST_SECRET_VAR", "my_secret")
	env := EnvMap{
		"DB_PASS": {Value: "${env:TEST_SECRET_VAR}"},
	}
	if err := ResolveEnv(env); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env["DB_PASS"].Value != "my_secret" {
		t.Errorf("DB_PASS = %q, want %q", env["DB_PASS"].Value, "my_secret")
	}
	// env: refs are NOT auto-secret
	if env["DB_PASS"].Secret {
		t.Error("env ref should not be auto-secret")
	}
}

func TestResolveEnv_EnvRefExplicitSecret(t *testing.T) {
	t.Setenv("TEST_SECRET_VAR", "my_secret")
	env := EnvMap{
		"DB_PASS": {Value: "${env:TEST_SECRET_VAR}", Secret: true},
	}
	if err := ResolveEnv(env); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !env["DB_PASS"].Secret {
		t.Error("explicit secret: true should be preserved")
	}
}

func TestResolveEnv_EnvRefMissing(t *testing.T) {
	env := EnvMap{
		"MISSING": {Value: "${env:DEFINITELY_NOT_SET_12345}"},
	}
	if err := ResolveEnv(env); err == nil {
		t.Fatal("expected error for missing env var")
	}
}

func TestResolveEnv_FileRef(t *testing.T) {
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("  file_secret_value  \n"), 0644); err != nil {
		t.Fatal(err)
	}

	env := EnvMap{
		"API_KEY": {Value: "${file:" + secretFile + "}"},
	}
	if err := ResolveEnv(env); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env["API_KEY"].Value != "file_secret_value" {
		t.Errorf("API_KEY = %q, want %q", env["API_KEY"].Value, "file_secret_value")
	}
	// file refs are auto-secret
	if !env["API_KEY"].Secret {
		t.Error("file ref should be auto-secret")
	}
}

func TestResolveEnv_FileRefMissing(t *testing.T) {
	env := EnvMap{
		"MISSING": {Value: "${file:/nonexistent/path/secret.txt}"},
	}
	if err := ResolveEnv(env); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestResolveEnv_MixedRefs(t *testing.T) {
	t.Setenv("TEST_HOST", "db.internal")
	t.Setenv("TEST_PORT", "5432")
	env := EnvMap{
		"DSN": {Value: "host=${env:TEST_HOST} port=${env:TEST_PORT}"},
	}
	if err := ResolveEnv(env); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env["DSN"].Value != "host=db.internal port=5432" {
		t.Errorf("DSN = %q", env["DSN"].Value)
	}
}

func TestResolveEnv_VaultRefFormat(t *testing.T) {
	// Vault ref without a running server should fail with clear message
	t.Setenv("VAULT_ADDR", "")
	env := EnvMap{
		"SECRET": {Value: "${vault:secret/data/myapp#api_key}"},
	}
	err := ResolveEnv(env)
	if err == nil {
		t.Fatal("expected error for vault ref without VAULT_ADDR")
	}
	if !contains(err.Error(), "VAULT_ADDR") {
		t.Errorf("error should mention VAULT_ADDR: %v", err)
	}
}

func TestResolveEnv_NilMap(t *testing.T) {
	if err := ResolveEnv(nil); err != nil {
		t.Fatalf("nil map should not error: %v", err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
