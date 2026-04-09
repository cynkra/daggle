package executor

import (
	"context"
	"fmt"

	"github.com/cynkra/daggle/dag"
)

// RmdExecutor renders R Markdown documents via rmarkdown::render().
type RmdExecutor struct{}

// Run generates R code to render the Rmd file and executes it via Rscript.
func (e *RmdExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	renderArgs := fmt.Sprintf("%q", step.Rmd)
	if step.OutputDir != "" {
		renderArgs += fmt.Sprintf(", output_dir = %q", step.OutputDir)
	}
	if step.OutputName != "" {
		renderArgs += fmt.Sprintf(", output_file = %q", step.OutputName)
	}

	rCode := wrapErrorOn(fmt.Sprintf(`if (!requireNamespace("rmarkdown", quietly = TRUE)) stop("step requires the rmarkdown package. Install with: install.packages('rmarkdown')")
cat("Rendering R Markdown...\n")
rmarkdown::render(%s)
cat("Render complete\n")
`, renderArgs), step.ErrorOn)

	return runRScript(ctx, rCode, step, logDir, workdir, env, "rmd")
}
