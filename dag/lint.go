package dag

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Diagnostic is a single lint finding. Line/Col are 1-based; 0 means unknown.
type Diagnostic struct {
	Path     string `json:"path"`
	Line     int    `json:"line,omitempty"`
	Col      int    `json:"col,omitempty"`
	Severity string `json:"severity"` // "error" | "warning" | "info"
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// NotifyChannels names the notification channels defined in the loaded config.
// Callers that don't have a config loaded should pass nil to Lint, which skips
// the notify-channel check entirely.
type NotifyChannels struct {
	All  map[string]bool // every defined channel name
	SMTP map[string]bool // subset whose type is "smtp"
}

// Lint runs the structural lint checks on a parsed DAG: missing scripts,
// unresolved ${env:}/${file:} references, and — when channels is non-nil —
// unknown or wrong-typed notify: channel references. It does not parse the
// DAG; call ParseFile first and pass the error to DiagnoseParseError on
// failure.
func Lint(d *DAG, dagPath string, channels *NotifyChannels) []Diagnostic {
	var out []Diagnostic
	out = append(out, lintMissingScripts(d, dagPath)...)
	out = append(out, lintSecretRefs(d, dagPath)...)
	if channels != nil {
		out = append(out, lintNotifyChannels(d, dagPath, *channels)...)
	}
	return out
}

// DiagnoseParseError renders a ParseFile error as one "error" diagnostic per
// line. Use it when ParseFile fails, in place of Lint.
func DiagnoseParseError(dagPath string, err error) []Diagnostic {
	out := make([]Diagnostic, 0)
	for _, line := range splitErrLines(err.Error()) {
		out = append(out, Diagnostic{
			Path: dagPath, Severity: "error", Code: "parse", Message: line,
		})
	}
	return out
}

func lintMissingScripts(d *DAG, dagPath string) []Diagnostic {
	var out []Diagnostic
	for _, s := range d.Steps {
		workdir := d.ResolveWorkdir(s)
		resolve := func(p string) string {
			if p == "" {
				return ""
			}
			if filepath.IsAbs(p) {
				return p
			}
			return filepath.Join(workdir, p)
		}
		check := func(field, p string) {
			if p == "" {
				return
			}
			abs := resolve(p)
			if _, err := os.Stat(abs); err != nil {
				out = append(out, Diagnostic{
					Path: dagPath, Severity: "error", Code: "missing-script",
					Message: fmt.Sprintf("step %q: %s %q does not exist (resolved to %s)", s.ID, field, p, abs),
				})
			}
		}
		check("script", s.Script)
		check("validate", s.Validate)
		check("quarto", s.Quarto)
		check("rmd", s.Rmd)
		if s.Connect != nil {
			check("connect.path", s.Connect.Path)
		}
		if s.Database != nil {
			check("database.query_file", s.Database.QueryFile)
		}
		if s.Email != nil {
			check("email.body_file", s.Email.BodyFile)
			for i, a := range s.Email.Attach {
				check(fmt.Sprintf("email.attach[%d]", i), a)
			}
		}
	}
	return out
}

var lintSecretRe = regexp.MustCompile(`\$\{(env|file|vault):([^}]+)\}`)

func lintSecretRefs(d *DAG, dagPath string) []Diagnostic {
	var out []Diagnostic
	check := func(scope string, envMap EnvMap) {
		for k, v := range envMap {
			matches := lintSecretRe.FindAllStringSubmatch(v.Value, -1)
			for _, m := range matches {
				source, ref := m[1], m[2]
				switch source {
				case "env":
					if os.Getenv(ref) == "" {
						out = append(out, Diagnostic{
							Path: dagPath, Severity: "error", Code: "unresolved-secret",
							Message: fmt.Sprintf("%s.%s references ${env:%s}, but %s is not set in the environment", scope, k, ref, ref),
						})
					}
				case "file":
					if _, err := os.Stat(ref); err != nil {
						out = append(out, Diagnostic{
							Path: dagPath, Severity: "error", Code: "unresolved-secret",
							Message: fmt.Sprintf("%s.%s references ${file:%s}, but the file does not exist", scope, k, ref),
						})
					}
				case "vault":
					// Not checked — would require network + token. Surface as info.
					out = append(out, Diagnostic{
						Path: dagPath, Severity: "info", Code: "vault-ref",
						Message: fmt.Sprintf("%s.%s references ${vault:%s} — not checked by lint", scope, k, ref),
					})
				}
			}
		}
	}
	check("env", d.Env)
	for _, s := range d.Steps {
		check(fmt.Sprintf("step[%s].env", s.ID), s.Env)
	}
	return out
}

func lintNotifyChannels(d *DAG, dagPath string, channels NotifyChannels) []Diagnostic {
	if len(channels.All) == 0 {
		return nil
	}
	var out []Diagnostic
	check := func(h *Hook, where string) {
		if h == nil || h.Notify == "" {
			return
		}
		if !channels.All[h.Notify] {
			out = append(out, Diagnostic{
				Path: dagPath, Severity: "error", Code: "unknown-channel",
				Message: fmt.Sprintf("%s: notify channel %q is not defined in config.yaml", where, h.Notify),
			})
		}
	}
	check(d.OnSuccess, "on_success")
	check(d.OnFailure, "on_failure")
	check(d.OnExit, "on_exit")
	if d.Trigger != nil {
		check(d.Trigger.OnDeadline, "trigger.on_deadline")
	}
	for _, s := range d.Steps {
		if s.Approve != nil {
			check(s.Approve.Notify, fmt.Sprintf("step %q approve.notify", s.ID))
		}
		if s.Email != nil && s.Email.Channel != "" {
			switch {
			case !channels.All[s.Email.Channel]:
				out = append(out, Diagnostic{
					Path: dagPath, Severity: "error", Code: "unknown-channel",
					Message: fmt.Sprintf("step %q email.channel %q is not defined in config.yaml", s.ID, s.Email.Channel),
				})
			case !channels.SMTP[s.Email.Channel]:
				out = append(out, Diagnostic{
					Path: dagPath, Severity: "error", Code: "wrong-channel-type",
					Message: fmt.Sprintf("step %q email.channel %q is not an smtp channel", s.ID, s.Email.Channel),
				})
			}
		}
	}
	return out
}

// splitErrLines splits a multi-line error message into one trimmed line per
// entry, dropping empty lines and the leading "- " that Validate produces.
// Falls back to the original string if splitting would produce nothing.
func splitErrLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "- ")
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		out = []string{s}
	}
	return out
}
