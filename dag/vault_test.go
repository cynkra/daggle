package dag

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadVaultSecret_Success(t *testing.T) {
	// Mock Vault KV v2 server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "test-token" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.URL.Path != "/v1/secret/data/myapp" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"data": map[string]interface{}{
					"api_key": "vault-secret-value",
					"db_pass": "vault-db-pass",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	t.Setenv("VAULT_ADDR", server.URL)
	t.Setenv("VAULT_TOKEN", "test-token")

	val, err := readVaultSecret("secret/data/myapp#api_key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "vault-secret-value" {
		t.Errorf("value = %q, want %q", val, "vault-secret-value")
	}
}

func TestReadVaultSecret_FieldNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"data": map[string]interface{}{
					"other_key": "value",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	t.Setenv("VAULT_ADDR", server.URL)
	t.Setenv("VAULT_TOKEN", "test-token")

	_, err := readVaultSecret("secret/data/myapp#missing_field")
	if err == nil {
		t.Fatal("expected error for missing field")
	}
}

func TestReadVaultSecret_BadFormat(t *testing.T) {
	_, err := readVaultSecret("no-hash-separator")
	if err == nil {
		t.Fatal("expected error for bad ref format")
	}
}

func TestReadVaultSecret_NoAddr(t *testing.T) {
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("VAULT_TOKEN", "token")

	_, err := readVaultSecret("secret/data/myapp#key")
	if err == nil {
		t.Fatal("expected error when VAULT_ADDR not set")
	}
}

func TestReadVaultSecret_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer server.Close()

	t.Setenv("VAULT_ADDR", server.URL)
	t.Setenv("VAULT_TOKEN", "test-token")

	_, err := readVaultSecret("secret/data/myapp#key")
	if err == nil {
		t.Fatal("expected error for server error")
	}
}

func TestResolveEnv_VaultIntegration(t *testing.T) {
	// Full integration test with mock Vault
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"data": map[string]interface{}{
					"api_key": "resolved-from-vault",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	t.Setenv("VAULT_ADDR", server.URL)
	t.Setenv("VAULT_TOKEN", "test-token")

	env := EnvMap{
		"SECRET": {Value: "${vault:secret/data/myapp#api_key}"},
	}
	if err := ResolveEnv(env); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env["SECRET"].Value != "resolved-from-vault" {
		t.Errorf("SECRET = %q, want %q", env["SECRET"].Value, "resolved-from-vault")
	}
	if !env["SECRET"].Secret {
		t.Error("vault ref should be auto-secret")
	}
}
