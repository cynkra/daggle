package cli

import (
	"fmt"
	"path/filepath"

	"github.com/cynkra/daggle/internal/archive"
	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var archiveOutput string

var archiveCmd = &cobra.Command{
	Use:   "archive <dag> <run-id>",
	Short: "Bundle a run directory into a tamper-evident tar.gz",
	Long: `Create a .tar.gz of the run directory with an embedded SHA-256 manifest.
Pair with 'daggle verify' to detect modification or corruption.`,
	Args: cobra.ExactArgs(2),
	RunE: archiveRun,
}

func init() {
	archiveCmd.Flags().StringVarP(&archiveOutput, "output", "o", "", "output path (default: ./<dag>_<run-id>.tar.gz)")
	rootCmd.AddCommand(archiveCmd)
}

func archiveRun(_ *cobra.Command, args []string) error {
	applyOverrides()
	dagName, runID := args[0], args[1]

	run, err := state.FindRun(dagName, runID)
	if err != nil {
		return err
	}

	out := archiveOutput
	if out == "" {
		out = fmt.Sprintf("%s_%s.tar.gz", dagName, run.ID)
	}
	if !filepath.IsAbs(out) {
		out, err = filepath.Abs(out)
		if err != nil {
			return fmt.Errorf("resolve output path: %w", err)
		}
	}

	res, err := archive.Create(run.Dir, out)
	if err != nil {
		return fmt.Errorf("archive: %w", err)
	}

	fmt.Printf("Archive: %s\n", res.OutPath)
	fmt.Printf("  files: %d\n", res.Files)
	fmt.Printf("  bytes: %d (uncompressed)\n", res.Bytes)
	return nil
}
