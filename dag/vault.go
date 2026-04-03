package dag

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// readVaultSecret reads a secret from HashiCorp Vault KV v2.
// The ref format is "path#field", e.g. "secret/data/myapp#api_key".
// Auth uses VAULT_ADDR and VAULT_TOKEN env vars (or ~/.vault-token).
func readVaultSecret(ref string) (string, error) {
	parts := strings.SplitN(ref, "#", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid vault reference %q: expected path#field", ref)
	}
	path := parts[0]
	field := parts[1]

	addr := os.Getenv("VAULT_ADDR")
	if addr == "" {
		return "", fmt.Errorf("VAULT_ADDR environment variable is not set")
	}

	token := os.Getenv("VAULT_TOKEN")
	if token == "" {
		// Fall back to ~/.vault-token
		home, err := os.UserHomeDir()
		if err == nil {
			data, err := os.ReadFile(filepath.Join(home, ".vault-token"))
			if err == nil {
				token = strings.TrimSpace(string(data))
			}
		}
	}
	if token == "" {
		return "", fmt.Errorf("VAULT_TOKEN environment variable is not set and ~/.vault-token not found")
	}

	url := strings.TrimRight(addr, "/") + "/v1/" + path

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("X-Vault-Token", token)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("vault request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read vault response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("vault returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse KV v2 response: {"data": {"data": {"field": "value"}}}
	var result struct {
		Data struct {
			Data map[string]interface{} `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse vault response: %w", err)
	}

	val, ok := result.Data.Data[field]
	if !ok {
		return "", fmt.Errorf("field %q not found in vault secret at %q", field, path)
	}

	strVal, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("field %q in vault secret is not a string", field)
	}

	return strVal, nil
}
