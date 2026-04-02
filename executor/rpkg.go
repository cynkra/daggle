package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cynkra/daggle/dag"
)

// RPkgExecutor runs R package development actions via Rscript.
type RPkgExecutor struct {
	Action string // "test", "check", "document", "lint", "style", "renv_restore", "coverage"
}

// Run generates R code for the given action and executes it via Rscript.
func (e *RPkgExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	pkgPath := e.resolvePkgPath(step)
	rCode := e.generateRCode(pkgPath)

	tmpFile := filepath.Join(logDir, step.ID+".rpkg.R")
	if err := os.WriteFile(tmpFile, []byte(rCode), 0644); err != nil {
		return Result{ExitCode: -1, Err: fmt.Errorf("write rpkg R: %w", err)}
	}

	cmd := exec.CommandContext(ctx, "Rscript", "--no-save", "--no-restore", tmpFile)
	return runProcess(ctx, cmd, step.ID, logDir, workdir, env)
}

func (e *RPkgExecutor) resolvePkgPath(step dag.Step) string {
	var raw string
	switch e.Action {
	case "test":
		raw = step.Test
	case "check":
		raw = step.Check
	case "document":
		raw = step.Document
	case "lint":
		raw = step.Lint
	case "style":
		raw = step.Style
	case "renv_restore":
		raw = step.RenvRestore
	case "coverage":
		raw = step.Coverage
	}
	if raw == "" || raw == "true" || raw == "." {
		return "."
	}
	return raw
}

func (e *RPkgExecutor) generateRCode(pkgPath string) string {
	switch e.Action {
	case "test":
		return fmt.Sprintf(`if (!requireNamespace("devtools", quietly = TRUE)) stop("step requires the devtools package. Install with: install.packages('devtools')")
cat("Running tests...\n")
results <- devtools::test(%q, stop_on_failure = FALSE)
failed <- sum(as.data.frame(results)$failed)
cat(sprintf("::daggle-output name=test_failures::%%d\n", failed))
if (failed > 0) stop(sprintf("%%d test(s) failed", failed))
`, pkgPath)

	case "check":
		return fmt.Sprintf(`if (!requireNamespace("rcmdcheck", quietly = TRUE)) stop("step requires the rcmdcheck package. Install with: install.packages('rcmdcheck')")
cat("Running R CMD check...\n")
res <- rcmdcheck::rcmdcheck(%q, args = "--no-manual", error_on = "warning")
cat(sprintf("::daggle-output name=check_errors::%%d\n", length(res$errors)))
cat(sprintf("::daggle-output name=check_warnings::%%d\n", length(res$warnings)))
cat(sprintf("::daggle-output name=check_notes::%%d\n", length(res$notes)))
`, pkgPath)

	case "document":
		return fmt.Sprintf(`if (!requireNamespace("roxygen2", quietly = TRUE)) stop("step requires the roxygen2 package. Install with: install.packages('roxygen2')")
cat("Generating documentation...\n")
roxygen2::roxygenize(%q)
cat("Documentation complete\n")
`, pkgPath)

	case "lint":
		return fmt.Sprintf(`if (!requireNamespace("lintr", quietly = TRUE)) stop("step requires the lintr package. Install with: install.packages('lintr')")
cat("Running linter...\n")
lints <- lintr::lint_package(%q)
cat(sprintf("::daggle-output name=lint_issues::%%d\n", length(lints)))
if (length(lints) > 0) {
  print(lints)
  stop(sprintf("%%d lint issue(s) found", length(lints)))
}
cat("No lint issues found\n")
`, pkgPath)

	case "style":
		return fmt.Sprintf(`if (!requireNamespace("styler", quietly = TRUE)) stop("step requires the styler package. Install with: install.packages('styler')")
cat("Styling code...\n")
res <- styler::style_pkg(%q)
changed <- sum(res$changed)
cat(sprintf("::daggle-output name=files_changed::%%d\n", changed))
cat(sprintf("Styled %%d file(s)\n", changed))
`, pkgPath)

	case "renv_restore":
		return fmt.Sprintf(`if (!requireNamespace("renv", quietly = TRUE)) stop("step requires the renv package. Install with: install.packages('renv')")
cat("Restoring renv library...\n")
renv::restore(project = %q, prompt = FALSE)
cat("renv restore complete\n")
`, pkgPath)

	case "coverage":
		return fmt.Sprintf(`if (!requireNamespace("covr", quietly = TRUE)) stop("step requires the covr package. Install with: install.packages('covr')")
cat("Measuring code coverage...\n")
cov <- covr::package_coverage(%q)
pct <- covr::percent_coverage(cov)
cat(sprintf("Coverage: %%.1f%%%%\n", pct))
cat(sprintf("::daggle-output name=coverage_pct::%%.1f\n", pct))
`, pkgPath)

	default:
		return fmt.Sprintf("stop('unknown rpkg action: %s')\n", e.Action)
	}
}
