// Package envutil contains small helpers for normalising the environment
// passed to spawned subprocesses.
package envutil

import "strings"

// utf8Locale is the locale value injected when the inherited LANG/LC_ALL is
// missing, empty, "C", or "POSIX". C.UTF-8 ships with glibc 2.13+, is built
// into Alpine and modern macOS, and needs no locale-gen step.
const utf8Locale = "C.UTF-8"

// WithUTF8Locale returns a copy of env with LANG and LC_ALL guaranteed to
// resolve to a UTF-8 locale. Existing values that are non-empty and not
// "C" or "POSIX" are preserved; missing or broken values are replaced
// with C.UTF-8.
//
// This prevents step subprocesses (R, Python, libc) from emitting non-ASCII
// characters as <U+nnnn> escapes when the daemon was launched with LANG=C
// — a common situation for systemd, cron, and CI runners.
func WithUTF8Locale(env []string) []string {
	out := make([]string, 0, len(env)+2)
	haveLang, haveLCAll := false, false
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "LANG="):
			haveLang = true
			if needsReplace(kv[len("LANG="):]) {
				out = append(out, "LANG="+utf8Locale)
				continue
			}
		case strings.HasPrefix(kv, "LC_ALL="):
			haveLCAll = true
			if needsReplace(kv[len("LC_ALL="):]) {
				out = append(out, "LC_ALL="+utf8Locale)
				continue
			}
		}
		out = append(out, kv)
	}
	if !haveLang {
		out = append(out, "LANG="+utf8Locale)
	}
	if !haveLCAll {
		out = append(out, "LC_ALL="+utf8Locale)
	}
	return out
}

func needsReplace(v string) bool {
	return v == "" || v == "C" || v == "POSIX"
}
