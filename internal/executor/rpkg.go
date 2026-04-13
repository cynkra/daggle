package executor

import (
	"context"
	"fmt"

	"github.com/cynkra/daggle/dag"
)

// RPkgExecutor runs R package development actions via Rscript.
type RPkgExecutor struct {
	Action string // "test", "check", "document", "lint", "style", "renv_restore", "coverage", "shinytest", "pkgdown", "install", "targets", "benchmark", "revdepcheck"
}

// Run generates R code for the given action and executes it via Rscript.
func (e *RPkgExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	pkgPath := e.resolvePkgPath(step)
	rCode := wrapErrorOn(e.generateRCode(pkgPath), step.ErrorOn)
	// rpkg steps don't pass step.Args, so use a step copy with empty args
	s := step
	s.Args = nil
	return runRScript(ctx, rCode, s, logDir, workdir, env, "rpkg")
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
	case "shinytest":
		raw = step.Shinytest
	case "pkgdown":
		raw = step.Pkgdown
	case "install":
		raw = step.Install
	case "targets":
		raw = step.Targets
	case "benchmark":
		raw = step.Benchmark
	case "revdepcheck":
		raw = step.Revdepcheck
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

	case "shinytest":
		return fmt.Sprintf(`if (!requireNamespace("shinytest2", quietly = TRUE)) stop("step requires the shinytest2 package. Install with: install.packages('shinytest2')")
cat("Running Shiny app tests...\n")
results <- shinytest2::test_app(%q)
cat("Shiny tests complete\n")
`, pkgPath)

	case "pkgdown":
		return fmt.Sprintf(`if (!requireNamespace("pkgdown", quietly = TRUE)) stop("step requires the pkgdown package. Install with: install.packages('pkgdown')")
cat("Building pkgdown site...\n")
pkgdown::build_site(%q)
cat("pkgdown site complete\n")
`, pkgPath)

	case "install":
		return fmt.Sprintf(`cat("Installing package...\n")
if (requireNamespace("pak", quietly = TRUE)) {
  pak::pkg_install(%q, ask = FALSE)
} else {
  install.packages(%q, repos = "https://cloud.r-project.org")
}
cat("Install complete\n")
`, pkgPath, pkgPath)

	case "targets":
		return fmt.Sprintf(`if (!requireNamespace("targets", quietly = TRUE)) stop("step requires the targets package. Install with: install.packages('targets')")
cat("Running targets pipeline...\n")
targets::tar_make(store = file.path(%q, "_targets"))
cat("Targets pipeline complete\n")
`, pkgPath)

	case "benchmark":
		return fmt.Sprintf(`if (!requireNamespace("bench", quietly = TRUE)) stop("step requires the bench package. Install with: install.packages('bench')")
cat("Running benchmarks...\n")
bench_files <- list.files(%q, pattern = "\\.[Rr]$", full.names = TRUE)
for (f in bench_files) {
  cat("Sourcing", f, "\n")
  source(f)
}
cat("Benchmarks complete\n")
`, pkgPath)

	case "revdepcheck":
		return fmt.Sprintf(`if (!requireNamespace("revdepcheck", quietly = TRUE)) stop("step requires the revdepcheck package. Install with: install.packages('revdepcheck')")
cat("Running reverse dependency checks...\n")
revdepcheck::revdep_check(%q, num_workers = 4)
results <- revdepcheck::revdep_summary()
cat("Reverse dependency checks complete\n")
`, pkgPath)

	default:
		return fmt.Sprintf("stop('unknown rpkg action: %s')\n", e.Action)
	}
}
