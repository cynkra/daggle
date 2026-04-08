package executor

import (
	"context"
	"fmt"

	"github.com/cynkra/daggle/dag"
)

// VetiverExecutor handles MLOps model versioning/deployment via the vetiver R package.
type VetiverExecutor struct{}

// Run generates R code for vetiver operations and executes it via Rscript.
func (e *VetiverExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	rCode := wrapErrorOn(generateVetiverR(step.Vetiver), step.ErrorOn)
	return runRScript(ctx, rCode, step, logDir, workdir, env, "vetiver")
}

func generateVetiverR(v *dag.VetiverDeploy) string {
	switch v.Action {
	case "pin":
		return fmt.Sprintf(`if (!requireNamespace("vetiver", quietly = TRUE)) stop("step requires the vetiver package. Install with: install.packages('vetiver')")
if (!requireNamespace("pins", quietly = TRUE)) stop("step requires the pins package. Install with: install.packages('pins')")

model <- readRDS(%q)
board <- pins::board_%s()
v <- vetiver::vetiver_model(model, %q)
cat("Pinning model", %q, "...\n")
vetiver::vetiver_pin_write(board, v)
cat("Model pinned successfully\n")
cat(sprintf("::daggle-output name=vetiver_model::%%s\n", %q))
`, v.Model, v.Board, v.Name, v.Name, v.Name)

	case "deploy":
		return fmt.Sprintf(`if (!requireNamespace("vetiver", quietly = TRUE)) stop("step requires the vetiver package. Install with: install.packages('vetiver')")
if (!requireNamespace("pins", quietly = TRUE)) stop("step requires the pins package. Install with: install.packages('pins')")

board <- pins::board_%s()
cat("Deploying model", %q, "to Connect...\n")
vetiver::vetiver_deploy_rsconnect(board, %q)
cat("Model deployed successfully\n")
cat(sprintf("::daggle-output name=vetiver_deployed::%%s\n", %q))
`, v.Board, v.Name, v.Name, v.Name)

	default:
		return fmt.Sprintf("stop('unknown vetiver action: %s')\n", v.Action)
	}
}
