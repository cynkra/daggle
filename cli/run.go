package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/engine"
	"github.com/cynkra/daggle/executor"
	"github.com/cynkra/daggle/renv"
	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

// Version is set via ldflags at build time.
var Version = "dev"

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

func runDAG(_ *cobra.Command, args []string) error {
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

	// Build reproducibility metadata
	dagHash, _ := dag.HashFile(dagPath)
	rVersion := detectRVersion()
	rPlatform := renv.DetectRPlatform()
	meta := &state.RunMeta{
		RunID:         run.ID,
		DAGName:       expanded.Name,
		DAGHash:       dagHash,
		DAGPath:       dagPath,
		StartTime:     time.Now(),
		RVersion:      rVersion,
		RPlatform:     rPlatform,
		Platform:      runtime.GOOS + "/" + runtime.GOARCH,
		Params:        params,
		DaggleVersion: Version,
	}

	// Detect renv
	projectDir := resolveProjectDir(expanded)
	renvInfo := renv.Detect(projectDir, rVersion, rPlatform)
	if renvInfo.Detected {
		meta.RenvDetected = true
		meta.RenvLibrary = renvInfo.LibraryPath
		if renvInfo.LibraryReady {
			fmt.Printf("Detected renv.lock — using library: %s\n", renvInfo.LibraryPath)
		} else {
			fmt.Printf("Warning: renv.lock found but library directory does not exist: %s\n", renvInfo.LibraryPath)
			fmt.Printf("  Run renv::restore() to install packages.\n")
		}
	}

	// Execute
	eng := engine.New(expanded, run, executor.New)
	eng.SetMeta(meta)
	if renvInfo.Detected && renvInfo.LibraryReady {
		eng.SetRenvLibPath(renvInfo.LibraryPath)
	}

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

func detectRVersion() string {
	out, err := exec.Command("Rscript", "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	// Rscript --version outputs something like "Rscript (R) version 4.4.1 (2024-06-14)"
	// or "R scripting front-end version 4.4.1 (2024-06-14)"
	s := strings.TrimSpace(string(bytes.TrimRight(out, "\n")))
	// Extract version number
	for _, line := range strings.Split(s, "\n") {
		if idx := strings.Index(line, "version "); idx >= 0 {
			rest := line[idx+8:]
			if sp := strings.IndexByte(rest, ' '); sp > 0 {
				return rest[:sp]
			}
			return rest
		}
	}
	return s
}

func resolveProjectDir(d *dag.DAG) string {
	if d.Workdir != "" {
		return d.Workdir
	}
	return d.SourceDir
}

func applyOverrides() {
	if dagsDir != "" {
		_ = os.Setenv("DAGGLE_DAGS_DIR", dagsDir)
	}
	if dataDir != "" {
		_ = os.Setenv("DAGGLE_DATA_DIR", dataDir)
	}
}
