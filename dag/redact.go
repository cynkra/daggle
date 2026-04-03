package dag

import "strings"

// Redactor replaces secret values with "***" in strings.
type Redactor struct {
	secrets []string
}

// NewRedactor creates a Redactor from an EnvMap, collecting all secret values.
func NewRedactor(envMaps ...EnvMap) *Redactor {
	var secrets []string
	for _, m := range envMaps {
		for _, v := range m {
			if v.Secret && v.Value != "" {
				secrets = append(secrets, v.Value)
			}
		}
	}
	return &Redactor{secrets: secrets}
}

// Redact replaces all known secret values in the string with "***".
func (r *Redactor) Redact(s string) string {
	if r == nil || len(r.secrets) == 0 {
		return s
	}
	for _, secret := range r.secrets {
		s = strings.ReplaceAll(s, secret, "***")
	}
	return s
}

// HasSecrets returns true if the redactor has any secret values to redact.
func (r *Redactor) HasSecrets() bool {
	return r != nil && len(r.secrets) > 0
}
