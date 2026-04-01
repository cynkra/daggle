package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/schochastics/rdag/dag"
	"github.com/schochastics/rdag/engine"
	"github.com/schochastics/rdag/executor"
	"github.com/schochastics/rdag/state"
	"github.com/spf13/cobra"
)

var runParams []string

var runCmd = &cobra.Command{
	Use:   "run <dag-name>",
	Short: "Run a DAG immediately",
	Args:  cobra.ExactArgs(1),
	RunE:  runDAG,
}

func init() {
	runCmd.Flags().StringArrayVarP(&runParams, "param", "p", nil, "parameter override (key=value)")
	rootCmd.AddCommand(runCmd)
}

func runDAG(cmd *cobra.Command, args []string) error {
	dagName := args[0]
	applyOverrides()

	// Resolve DAG file
	dagPath := resolveDAGPath(dagName)
	d, err := dag.ParseFile(dagPath)
	if err != nil {
		return fmt.Errorf("parse DAG %q: %w", dagName, err)
	}

	// Parse param overrides
	params := parseParams(runParams)

	// Expand templates
	expanded, err := dag.ExpandDAG(d, params)
	if err != nil {
		return fmt.Errorf("expand templates: %w", err)
	}

	// Create run
	run, err := state.CreateRun(expanded.Name)
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}

	fmt.Printf("Starting DAG %q (run %s)\n", expanded.Name, run.ID)
	fmt.Printf("Run directory: %s\n\n", run.Dir)

	// Set up context with signal handling
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Execute
	eng := engine.New(expanded, run, func(s dag.Step) executor.Executor {
		return executor.New(s)
	})

	if err := eng.Run(ctx); err != nil {
		fmt.Printf("\nDAG %q failed: %v\n", expanded.Name, err)
		return err
	}

	fmt.Printf("\nDAG %q completed successfully\n", expanded.Name)
	return nil
}

func resolveDAGPath(name string) string {
	// If it looks like a path, use it directly
	if strings.Contains(name, "/") || strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
		return name
	}
	// Otherwise look in the DAGs directory
	dir := state.DAGDir()
	// Try .yaml then .yml
	yamlPath := filepath.Join(dir, name+".yaml")
	if _, err := os.Stat(yamlPath); err == nil {
		return yamlPath
	}
	return filepath.Join(dir, name+".yml")
}

func parseParams(raw []string) map[string]string {
	params := make(map[string]string)
	for _, p := range raw {
		parts := strings.SplitN(p, "=", 2)
		if len(parts) == 2 {
			params[parts[0]] = parts[1]
		}
	}
	return params
}

func applyOverrides() {
	if dagsDir != "" {
		os.Setenv("RDAG_CONFIG_DIR", filepath.Dir(dagsDir))
	}
	if dataDir != "" {
		os.Setenv("RDAG_DATA_DIR", dataDir)
	}
}
