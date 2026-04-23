package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

func runLint(_ *cobra.Command, args []string) error {
	applyOverrides()
	dagPath := resolveDAGPath(args[0])
	absDAG, err := filepath.Abs(dagPath)
	if err != nil {
		absDAG = dagPath
	}

	diags := lintDAG(absDAG)

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

// lintDAG parses the DAG and runs every available lint check: the pure
// structural checks in dag.Lint, notify-channel resolution against the loaded
// config.yaml, and (when requested) the --check-packages Rscript probe.
func lintDAG(dagPath string) []dag.Diagnostic {
	d, err := dag.ParseFile(dagPath)
	if err != nil {
		return dag.DiagnoseParseError(dagPath, err)
	}
	diags := dag.Lint(d, dagPath, loadNotifyChannels())
	if lintCheckPackages {
		diags = append(diags, checkRequiredRPackages(d, dagPath)...)
	}
	return diags
}

// loadNotifyChannels builds a dag.NotifyChannels from the loaded config.
// Returns nil when config.yaml is missing or defines no channels, so callers
// know to skip the notify-channel check rather than flagging every reference.
func loadNotifyChannels() *dag.NotifyChannels {
	cfg, err := state.LoadConfig()
	if err != nil || len(cfg.Notifications) == 0 {
		return nil
	}
	nc := &dag.NotifyChannels{
		All:  make(map[string]bool, len(cfg.Notifications)),
		SMTP: make(map[string]bool, len(cfg.Notifications)),
	}
	for name, ch := range cfg.Notifications {
		nc.All[name] = true
		if ch.Type == "smtp" {
			nc.SMTP[name] = true
		}
	}
	return nc
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
func checkRequiredRPackages(d *dag.DAG, dagPath string) []dag.Diagnostic {
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
		return []dag.Diagnostic{{
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
		return []dag.Diagnostic{{
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

	var diags []dag.Diagnostic
	for _, p := range pkgs {
		if installed[p] {
			continue
		}
		steps := needed[p]
		sort.Strings(steps)
		diags = append(diags, dag.Diagnostic{
			Path: dagPath, Severity: "error", Code: "missing-r-package",
			Message: fmt.Sprintf("R package %q is not installed (needed by step(s): %s) — install with: install.packages(%q)",
				p, strings.Join(steps, ", "), p),
		})
	}
	return diags
}

// emitLintDiagnostics prints diagnostics in the requested format.
func emitLintDiagnostics(format string, diags []dag.Diagnostic) error {
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

func emitLintText(diags []dag.Diagnostic) error {
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

func emitLintGNU(diags []dag.Diagnostic) error {
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

func emitLintJSON(diags []dag.Diagnostic) error {
	if diags == nil {
		diags = []dag.Diagnostic{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(diags)
}

func countSeverity(diags []dag.Diagnostic, sev string) int {
	n := 0
	for _, d := range diags {
		if d.Severity == sev {
			n++
		}
	}
	return n
}
