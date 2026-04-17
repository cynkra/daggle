package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
)

// runRScript writes R code to a temp file and executes it via Rscript.
// The suffix is used to name the temp file (e.g. "inline", "rpkg", "rmd").
//
// The user's R code is wrapped in a tryCatch that captures sessionInfo()
// to {run_dir}/{step_id}.sessioninfo.json on error, then re-raises.
func runRScript(ctx context.Context, rCode string, step dag.Step, logDir, workdir string, env []string, suffix string) Result {
	tmpFile := filepath.Join(logDir, step.ID+"."+suffix+".R")
	wrapped := wrapRCodeWithSessionInfo(rCode, logDir, step.ID)
	if err := os.WriteFile(tmpFile, []byte(wrapped), 0o644); err != nil {
		return Result{ExitCode: -1, Err: fmt.Errorf("write %s R: %w", suffix, err)}
	}
	args := append([]string{"--no-save", "--no-restore", tmpFile}, step.Args...)
	cmd := exec.CommandContext(ctx, state.ToolPath("rscript"), args...)
	return runProcess(ctx, cmd, step.ID, logDir, workdir, env)
}

// wrapRCodeWithSessionInfo wraps R code in a tryCatch that writes sessionInfo
// to a JSON file on error, then re-raises so the step still fails.
func wrapRCodeWithSessionInfo(rCode, runDir, stepID string) string {
	sessionPath := filepath.Join(runDir, stepID+".sessioninfo.json")
	var b strings.Builder
	// Preamble: define handler, then open tryCatch around user code.
	fmt.Fprintf(&b, `local({
  .daggle_session_path <- %s
  .daggle_write_sessioninfo <- function(err) {
    tryCatch({
      si_txt <- paste(capture.output(print(sessionInfo())), collapse = "\n")
      esc <- function(s) {
        s <- gsub("\\\\", "\\\\\\\\", s)
        s <- gsub("\"", "\\\\\"", s)
        s <- gsub("\n", "\\n", s, fixed = TRUE)
        s <- gsub("\t", "\\t", s, fixed = TRUE)
        s <- gsub("\r", "\\r", s, fixed = TRUE)
        paste0("\"", s, "\"")
      }
      out <- paste0(
        "{",
        "\"r_version\":", esc(R.version.string), ",",
        "\"platform\":", esc(R.version$platform), ",",
        "\"error_message\":", esc(conditionMessage(err)), ",",
        "\"session_info\":", esc(si_txt), ",",
        "\"timestamp\":", esc(format(Sys.time(), "%%Y-%%m-%%dT%%H:%%M:%%SZ", tz = "UTC")),
        "}"
      )
      writeLines(out, .daggle_session_path)
    }, error = function(e2) invisible(NULL))
  }
  tryCatch({
`, rStringLiteral(sessionPath))
	b.WriteString(rCode)
	b.WriteString(`
  }, error = function(e) {
    .daggle_write_sessioninfo(e)
    stop(e)
  })
})
`)
	return b.String()
}

// rStringLiteral returns a single-quoted R string literal with backslashes and
// single quotes escaped.
func rStringLiteral(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}
