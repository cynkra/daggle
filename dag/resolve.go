package dag

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var secretRefRe = regexp.MustCompile(`\$\{(env|file|vault):([^}]+)\}`)

// ResolveEnv resolves secret references in env var values.
// Supported references:
//   - ${env:VAR} — read from process environment
//   - ${file:/path} — read file contents (trimmed)
//   - ${vault:path#field} — read from HashiCorp Vault KV v2
//
// References that use vault or file sources are automatically marked as secret.
// Unresolved references (missing env var, missing file, vault error) cause an error.
func ResolveEnv(env EnvMap) error {
	for k, v := range env {
		resolved, isSecret, err := resolveValue(v.Value)
		if err != nil {
			return fmt.Errorf("env %q: %w", k, err)
		}
		env[k] = EnvVar{
			Value:  resolved,
			Secret: v.Secret || isSecret,
		}
	}
	return nil
}

// resolveValue resolves all ${...} references in a string.
// Returns the resolved value and whether any auto-secret source was used.
func resolveValue(s string) (string, bool, error) {
	if !strings.Contains(s, "${") {
		return s, false, nil
	}

	var autoSecret bool
	var resolveErr error

	result := secretRefRe.ReplaceAllStringFunc(s, func(match string) string {
		if resolveErr != nil {
			return match
		}

		parts := secretRefRe.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}

		source := parts[1]
		ref := parts[2]

		switch source {
		case "env":
			val := os.Getenv(ref)
			if val == "" {
				resolveErr = fmt.Errorf("${env:%s}: environment variable %q is not set", ref, ref)
				return match
			}
			return val

		case "file":
			data, err := os.ReadFile(ref)
			if err != nil {
				resolveErr = fmt.Errorf("${file:%s}: %w", ref, err)
				return match
			}
			autoSecret = true
			return strings.TrimSpace(string(data))

		case "vault":
			val, err := readVaultSecret(ref)
			if err != nil {
				resolveErr = fmt.Errorf("${vault:%s}: %w", ref, err)
				return match
			}
			autoSecret = true
			return val

		default:
			resolveErr = fmt.Errorf("unknown secret source %q in ${%s:%s}", source, source, ref)
			return match
		}
	})

	if resolveErr != nil {
		return "", false, resolveErr
	}

	return result, autoSecret, nil
}
