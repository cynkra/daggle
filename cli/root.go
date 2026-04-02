package cli

import (
	"github.com/spf13/cobra"
)

var (
	dagsDir string
	dataDir string
)

var rootCmd = &cobra.Command{
	Use:   "daggle",
	Short: "A lightweight DAG scheduler for R",
	Long:  "daggle is a local-first, file-based DAG scheduler designed for R workflows.",
}

func init() {
	rootCmd.PersistentFlags().StringVar(&dagsDir, "dags-dir", "", "override DAG definitions directory")
	rootCmd.PersistentFlags().StringVar(&dataDir, "data-dir", "", "override data/runs directory")
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}
