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

// RedactBytes returns a copy of b with secret values replaced. Used when
// streaming subprocess log files where allocating a string per chunk is
// fine (logs are read in full, not in tight loops).
func (r *Redactor) RedactBytes(b []byte) []byte {
	if r == nil || len(r.secrets) == 0 {
		return b
	}
	return []byte(r.Redact(string(b)))
}

// HasSecrets returns true if the redactor has any secret values to redact.
func (r *Redactor) HasSecrets() bool {
	return r != nil && len(r.secrets) > 0
}

// AllEnvMaps returns the DAG-level env plus every step's env. Useful for
// callers that need the full set of secret-bearing maps to build a Redactor.
func AllEnvMaps(d *DAG) []EnvMap {
	if d == nil {
		return nil
	}
	maps := make([]EnvMap, 0, 1+len(d.Steps))
	maps = append(maps, d.Env)
	for i := range d.Steps {
		if len(d.Steps[i].Env) > 0 {
			maps = append(maps, d.Steps[i].Env)
		}
	}
	return maps
}

// LoadRedactor parses the DAG at path, resolves all env (DAG + step level),
// and returns a Redactor for the resolved secret values. Used by view-time
// callers (HTTP step-log, SSE stream, `daggle why`) that don't have a
// redactor in scope.
//
// On any error (parse failure, env resolution failure — e.g. a vault ref
// that can't be re-fetched), a non-nil empty Redactor is returned alongside
// the error so callers can serve their response without redaction rather
// than failing outright. Callers that care about partial-redaction risk
// should log the error.
func LoadRedactor(path string) (*Redactor, error) {
	d, err := LoadAndExpand(path, nil)
	if err != nil {
		return &Redactor{}, err
	}
	if err := ResolveEnv(d.Env); err != nil {
		return &Redactor{}, err
	}
	for i := range d.Steps {
		if len(d.Steps[i].Env) == 0 {
			continue
		}
		if err := ResolveEnv(d.Steps[i].Env); err != nil {
			return &Redactor{}, err
		}
	}
	return NewRedactor(AllEnvMaps(d)...), nil
}
