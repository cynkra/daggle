package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var (
	lintFormat        string
	lintCheckPackages bool
)

var lintCmd = &cobra.Command{
	Use:   "lint <dag-name|path>",
	Short: "Semantic diagnostics for a DAG",
	Long: `Run semantic checks on a DAG beyond schema validation:

  - Missing scripts referenced by script/validate/quarto/rmd/connect steps
  - Unresolvable ${env:}, ${file:}, ${vault:} secret references
  - Unknown notify: channel names (requires config.yaml)
  - Optional: required R packages installed (--check-packages)

Output formats:
  text  human-readable (default)
  gnu   path:line:col: severity: message [code] — for editor integration
  json  array of diagnostics — for tooling

Exits 1 if any error-severity diagnostic is reported.`,
	Args: cobra.ExactArgs(1),
	RunE: runLint,
}

func init() {
	lintCmd.Flags().StringVar(&lintFormat, "format", "text", "output format: text, gnu, json")
	lintCmd.Flags().BoolVar(&lintCheckPackages, "check-packages", false, "also check that required R packages are installed (runs Rscript)")
	rootCmd.AddCommand(lintCmd)
}

// lintDiagnostic is a single lint finding. Line/Col are 1-based; 0 means unknown.
type lintDiagnostic struct {
	Path     string `json:"path"`
	Line     int    `json:"line,omitempty"`
	Col      int    `json:"col,omitempty"`
	Severity string `json:"severity"` // "error" | "warning" | "info"
	Code     string `json:"code"`
	Message  string `json:"message"`
}

func runLint(_ *cobra.Command, args []string) error {
	applyOverrides()
	dagPath := resolveDAGPath(args[0])
	absDAG, err := filepath.Abs(dagPath)
	if err != nil {
		absDAG = dagPath
	}

	diags := collectLintDiagnostics(absDAG)
	if lintCheckPackages {
		diags = append(diags, checkRequiredRPackages(absDAG)...)
	}

	sort.Slice(diags, func(i, j int) bool {
		if diags[i].Path != diags[j].Path {
			return diags[i].Path < diags[j].Path
		}
		if diags[i].Line != diags[j].Line {
			return diags[i].Line < diags[j].Line
		}
		return diags[i].Code < diags[j].Code
	})

	if err := emitLintDiagnostics(lintFormat, diags); err != nil {
		return err
	}

	for _, d := range diags {
		if d.Severity == "error" {
			return fmt.Errorf("%d error(s) found", countSeverity(diags, "error"))
		}
	}
	return nil
}

// collectLintDiagnostics gathers all lint findings for a DAG file.
func collectLintDiagnostics(dagPath string) []lintDiagnostic {
	var out []lintDiagnostic

	d, err := dag.ParseFile(dagPath)
	if err != nil {
		// Parse/validation failure — report each line of the error as a diagnostic.
		for _, line := range splitErr(err.Error()) {
			out = append(out, lintDiagnostic{
				Path: dagPath, Severity: "error", Code: "parse", Message: line,
			})
		}
		return out
	}

	out = append(out, lintMissingScripts(d, dagPath)...)
	out = append(out, lintSecretRefs(d, dagPath)...)
	out = append(out, lintNotifyChannels(d, dagPath)...)

	return out
}

func lintMissingScripts(d *dag.DAG, dagPath string) []lintDiagnostic {
	var out []lintDiagnostic
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
				out = append(out, lintDiagnostic{
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
	}
	return out
}

var lintSecretRe = regexp.MustCompile(`\$\{(env|file|vault):([^}]+)\}`)

func lintSecretRefs(d *dag.DAG, dagPath string) []lintDiagnostic {
	var out []lintDiagnostic
	check := func(scope string, envMap dag.EnvMap) {
		for k, v := range envMap {
			matches := lintSecretRe.FindAllStringSubmatch(v.Value, -1)
			for _, m := range matches {
				source, ref := m[1], m[2]
				switch source {
				case "env":
					if os.Getenv(ref) == "" {
						out = append(out, lintDiagnostic{
							Path: dagPath, Severity: "error", Code: "unresolved-secret",
							Message: fmt.Sprintf("%s.%s references ${env:%s}, but %s is not set in the environment", scope, k, ref, ref),
						})
					}
				case "file":
					if _, err := os.Stat(ref); err != nil {
						out = append(out, lintDiagnostic{
							Path: dagPath, Severity: "error", Code: "unresolved-secret",
							Message: fmt.Sprintf("%s.%s references ${file:%s}, but the file does not exist", scope, k, ref),
						})
					}
				case "vault":
					// Not checked — would require network + token. Leave as info.
					out = append(out, lintDiagnostic{
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

func lintNotifyChannels(d *dag.DAG, dagPath string) []lintDiagnostic {
	cfg, err := state.LoadConfig()
	if err != nil {
		return nil
	}
	if len(cfg.Notifications) == 0 {
		// No config.yaml or no channels — silently skip rather than flagging every reference.
		return nil
	}
	channels := make(map[string]bool, len(cfg.Notifications))
	for name := range cfg.Notifications {
		channels[name] = true
	}

	var out []lintDiagnostic
	check := func(h *dag.Hook, where string) {
		if h == nil || h.Notify == "" {
			return
		}
		if !channels[h.Notify] {
			out = append(out, lintDiagnostic{
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
	}
	return out
}

// requiredRPackagesByStepType maps step types to R packages that must be
// installed for the step to run. Mirrors the requireNamespace checks inside
// internal/executor/.
var requiredRPackagesByStepType = map[string]string{
	"test":        "testthat",
	"check":       "rcmdcheck",
	"document":    "roxygen2",
	"lint":        "lintr",
	"style":       "styler",
	"coverage":    "covr",
	"rmd":         "rmarkdown",
	"shinytest":   "shinytest2",
	"pkgdown":     "pkgdown",
	"targets":     "targets",
	"benchmark":   "bench",
	"revdepcheck": "revdepcheck",
	"pin":         "pins",
	"vetiver":     "vetiver",
}

// checkRequiredRPackages runs one Rscript call to check whether all packages
// needed by this DAG's steps are installed. Skips entirely if no steps need R
// packages or if Rscript cannot be located.
func checkRequiredRPackages(dagPath string) []lintDiagnostic {
	d, err := dag.ParseFile(dagPath)
	if err != nil {
		return nil // parse errors already reported by collectLintDiagnostics
	}
	needed := make(map[string][]string) // pkg -> step IDs
	for _, s := range d.Steps {
		if pkg := requiredRPackagesByStepType[dag.StepType(s)]; pkg != "" {
			needed[pkg] = append(needed[pkg], s.ID)
		}
	}
	if len(needed) == 0 {
		return nil
	}

	rscript := state.ToolPath("rscript")
	if _, err := exec.LookPath(rscript); err != nil {
		return []lintDiagnostic{{
			Path: dagPath, Severity: "warning", Code: "r-not-found",
			Message: fmt.Sprintf("--check-packages requested but Rscript not found: %v", err),
		}}
	}

	pkgs := make([]string, 0, len(needed))
	for p := range needed {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	quoted := make([]string, len(pkgs))
	for i, p := range pkgs {
		quoted[i] = fmt.Sprintf("%q", p)
	}
	rCode := fmt.Sprintf(
		`pkgs <- c(%s); for (p in pkgs) cat(p, if (requireNamespace(p, quietly=TRUE)) "yes" else "no", "\n", sep=" ")`,
		strings.Join(quoted, ", "),
	)
	cmd := exec.Command(rscript, "--no-save", "--no-restore", "-e", rCode)
	out, err := cmd.Output()
	if err != nil {
		return []lintDiagnostic{{
			Path: dagPath, Severity: "warning", Code: "r-check-failed",
			Message: fmt.Sprintf("--check-packages: Rscript invocation failed: %v", err),
		}}
	}

	installed := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 {
			installed[parts[0]] = parts[1] == "yes"
		}
	}

	var diags []lintDiagnostic
	for _, p := range pkgs {
		if installed[p] {
			continue
		}
		steps := needed[p]
		sort.Strings(steps)
		diags = append(diags, lintDiagnostic{
			Path: dagPath, Severity: "error", Code: "missing-r-package",
			Message: fmt.Sprintf("R package %q is not installed (needed by step(s): %s) — install with: install.packages(%q)",
				p, strings.Join(steps, ", "), p),
		})
	}
	return diags
}

// emitLintDiagnostics prints diagnostics in the requested format.
func emitLintDiagnostics(format string, diags []lintDiagnostic) error {
	switch format {
	case "", "text":
		return emitLintText(diags)
	case "gnu":
		return emitLintGNU(diags)
	case "json":
		return emitLintJSON(diags)
	default:
		return fmt.Errorf("unknown --format %q (expected: text, gnu, json)", format)
	}
}

func emitLintText(diags []lintDiagnostic) error {
	if len(diags) == 0 {
		fmt.Println("No issues found.")
		return nil
	}
	counts := map[string]int{}
	for _, d := range diags {
		counts[d.Severity]++
		loc := d.Path
		if d.Line > 0 {
			loc = fmt.Sprintf("%s:%d", d.Path, d.Line)
		}
		fmt.Printf("[%s] %s: %s (%s)\n", strings.ToUpper(d.Severity), loc, d.Message, d.Code)
	}
	fmt.Printf("\n%d error(s), %d warning(s), %d info\n",
		counts["error"], counts["warning"], counts["info"])
	return nil
}

func emitLintGNU(diags []lintDiagnostic) error {
	for _, d := range diags {
		line, col := d.Line, d.Col
		if line <= 0 {
			line = 1
		}
		if col <= 0 {
			col = 1
		}
		fmt.Printf("%s:%d:%d: %s: %s [%s]\n", d.Path, line, col, d.Severity, d.Message, d.Code)
	}
	return nil
}

func emitLintJSON(diags []lintDiagnostic) error {
	if diags == nil {
		diags = []lintDiagnostic{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(diags)
}

func countSeverity(diags []lintDiagnostic, sev string) int {
	n := 0
	for _, d := range diags {
		if d.Severity == sev {
			n++
		}
	}
	return n
}

func splitErr(s string) []string {
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
