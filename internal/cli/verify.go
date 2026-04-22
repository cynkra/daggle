package cli

import (
	"fmt"
	"os"

	"github.com/cynkra/daggle/internal/archive"
	"github.com/spf13/cobra"
)

var verifyCmd = &cobra.Command{
	Use:   "verify <archive>",
	Short: "Check the integrity of a daggle archive",
	Long: `Recompute SHA-256 hashes of every file in the archive and compare
against the embedded manifest. Exits non-zero if any file is mismatched,
missing, or extra.`,
	Args: cobra.ExactArgs(1),
	RunE: verifyArchive,
}

func init() {
	rootCmd.AddCommand(verifyCmd)
}

func verifyArchive(_ *cobra.Command, args []string) error {
	path := args[0]
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("archive %q: %w", path, err)
	}

	report, err := archive.Verify(path)
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}

	if report.OK() {
		fmt.Printf("OK: %d files verified\n", report.Files)
		return nil
	}

	fmt.Printf("FAIL: %s\n", path)
	if len(report.Mismatched) > 0 {
		fmt.Printf("  mismatched (%d):\n", len(report.Mismatched))
		for _, n := range report.Mismatched {
			fmt.Printf("    %s\n", n)
		}
	}
	if len(report.Missing) > 0 {
		fmt.Printf("  missing (%d):\n", len(report.Missing))
		for _, n := range report.Missing {
			fmt.Printf("    %s\n", n)
		}
	}
	if len(report.Extra) > 0 {
		fmt.Printf("  extra (%d):\n", len(report.Extra))
		for _, n := range report.Extra {
			fmt.Printf("    %s\n", n)
		}
	}
	return fmt.Errorf("archive verification failed")
}
