package cli

import (
	"time"

	"github.com/cynkra/daggle/internal/executor"
	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var (
	dagsDir   string
	dataDir   string
	globalCfg state.Config
)

var rootCmd = &cobra.Command{
	Use:   "daggle",
	Short: "A lightweight DAG scheduler for R",
	Long:  "daggle is a local-first, file-based DAG scheduler designed for R workflows.",
	PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
		applyOverrides()
		cfg, _ := state.LoadConfig()
		globalCfg = cfg
		state.InitTools(cfg)
		applyEngineConfig(cfg.Engine)
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&dagsDir, "dags-dir", "", "override DAG definitions directory")
	rootCmd.PersistentFlags().StringVar(&dataDir, "data-dir", "", "override data/runs directory")
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

// applyEngineConfig sets executor package-level defaults from config.
func applyEngineConfig(cfg state.EngineConfig) {
	if cfg.GracePeriod != "" {
		if d, err := time.ParseDuration(cfg.GracePeriod); err == nil {
			executor.GracePeriod = d
		}
	}
	if cfg.ErrorContextLines > 0 {
		executor.ErrorContextLines = cfg.ErrorContextLines
	}
}
