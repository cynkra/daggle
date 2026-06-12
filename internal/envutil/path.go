package envutil

import (
	"os"
	"strings"
)

// WithToolDirsOnPath returns a copy of env with dirs prepended to the PATH
// variable. Directories already present on PATH are skipped, and duplicate
// input dirs are collapsed; relative order of the dirs that are added is
// preserved. If env has no PATH entry one is created.
//
// daggle resolves absolute paths for the tools it invokes directly (Rscript,
// quarto, sh, …), so it finds them regardless of PATH. But those tools may
// spawn *further* subprocesses via their own PATH lookup — most notably Quarto,
// which searches PATH for Rscript to execute R chunks. When the daemon is
// launched with a minimal PATH (launchd's /usr/bin:/bin:/usr/sbin:/sbin,
// systemd, cron), that nested lookup fails with "Unable to locate an installed
// version of R" even though daggle's own R steps work. Prepending the resolved
// tool directories onto PATH makes the nested lookup succeed.
func WithToolDirsOnPath(env []string, dirs []string) []string {
	// Ordered, de-duplicated list of candidate dirs.
	var prepend []string
	seen := make(map[string]bool, len(dirs))
	for _, d := range dirs {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		prepend = append(prepend, d)
	}
	if len(prepend) == 0 {
		return env
	}

	sep := string(os.PathListSeparator)
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		if !found && strings.HasPrefix(kv, "PATH=") {
			found = true
			existing := kv[len("PATH="):]
			onPath := make(map[string]bool)
			for _, p := range strings.Split(existing, sep) {
				onPath[p] = true
			}
			var add []string
			for _, d := range prepend {
				if !onPath[d] {
					add = append(add, d)
				}
			}
			if len(add) == 0 {
				out = append(out, kv)
				continue
			}
			newPath := strings.Join(add, sep)
			if existing != "" {
				newPath += sep + existing
			}
			out = append(out, "PATH="+newPath)
			continue
		}
		out = append(out, kv)
	}
	if !found {
		out = append(out, "PATH="+strings.Join(prepend, sep))
	}
	return out
}
