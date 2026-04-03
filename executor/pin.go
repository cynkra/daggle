package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cynkra/daggle/dag"
)

// PinExecutor publishes data/models via the pins R package.
type PinExecutor struct{}

// Run generates R code to pin an object and executes it via Rscript.
func (e *PinExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	p := step.Pin
	rCode := wrapErrorOn(generatePinR(p), step.ErrorOn)

	tmpFile := filepath.Join(logDir, step.ID+".pin.R")
	if err := os.WriteFile(tmpFile, []byte(rCode), 0644); err != nil {
		return Result{ExitCode: -1, Err: fmt.Errorf("write pin R: %w", err)}
	}

	cmd := exec.CommandContext(ctx, "Rscript", "--no-save", "--no-restore", tmpFile)
	return runProcess(ctx, cmd, step.ID, logDir, workdir, env)
}

func generatePinR(p *dag.PinDeploy) string {
	versioned := "TRUE"
	if p.Versioned != nil && !*p.Versioned {
		versioned = "FALSE"
	}

	pinType := p.Type
	if pinType == "" {
		pinType = "rds"
	}

	return fmt.Sprintf(`if (!requireNamespace("pins", quietly = TRUE)) stop("step requires the pins package. Install with: install.packages('pins')")

board <- pins::board_%s()
obj <- readRDS(%q)
cat("Pinning", %q, "to", %q, "board...\n")
pins::pin_write(board, obj, name = %q, type = %q, versioned = %s)
cat("Pin complete\n")
cat(sprintf("::daggle-output name=pin_name::%%s\n", %q))
`, p.Board, p.Object, p.Name, p.Board, p.Name, pinType, versioned, p.Name)
}
