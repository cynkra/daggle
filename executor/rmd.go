package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cynkra/daggle/dag"
)

// RmdExecutor renders R Markdown documents via rmarkdown::render().
type RmdExecutor struct{}

// Run generates R code to render the Rmd file and executes it via Rscript.
func (e *RmdExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	rCode := fmt.Sprintf(`if (!requireNamespace("rmarkdown", quietly = TRUE)) stop("step requires the rmarkdown package. Install with: install.packages('rmarkdown')")
cat("Rendering R Markdown...\n")
rmarkdown::render(%q)
cat("Render complete\n")
`, step.Rmd)

	tmpFile := filepath.Join(logDir, step.ID+".rmd.R")
	if err := os.WriteFile(tmpFile, []byte(rCode), 0644); err != nil {
		return Result{ExitCode: -1, Err: fmt.Errorf("write rmd R: %w", err)}
	}

	cmd := exec.CommandContext(ctx, "Rscript", "--no-save", "--no-restore", tmpFile)
	return runProcess(ctx, cmd, step.ID, logDir, workdir, env)
}
