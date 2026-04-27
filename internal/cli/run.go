package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/cynkra/daggle/cache"
	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/internal/engine"
	"github.com/cynkra/daggle/internal/executor"
	"github.com/cynkra/daggle/renv"
	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

// Version is set via ldflags at build time.
var Version = "dev"

var runParams []string
var runDryRun bool

var runCmd = &cobra.Command{
	Use:   "run <dag-name>",
	Short: "Run a DAG immediately",
	Args:  cobra.ExactArgs(1),
	RunE:  runDAG,
}

func init() {
	runCmd.Flags().StringArrayVarP(&runParams, "param", "p", nil, "parameter override (key=value)")
	runCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "validate the DAG and report what would happen without executing or creating a run")
	rootCmd.AddCommand(runCmd)
}

func runDAG(_ *cobra.Command, args []string) error {
	dagName := args[0]
	applyOverrides()

	// Resolve DAG file
	dagPath := resolveDAGPath(dagName)

	// Parse param overrides
	params := parseParams(runParams)

	// Dry-run: report what would happen without creating a run or executing.
	if runDryRun {
		expanded, err := dag.LoadAndExpand(dagPath, params)
		if err != nil {
			return fmt.Errorf("DAG %q: %w", dagName, err)
		}
		if err := dag.ResolveEnv(expanded.Env); err != nil {
			return fmt.Errorf("resolve env: %w", err)
		}
		for i := range expanded.Steps {
			if err := dag.ResolveEnv(expanded.Steps[i].Env); err != nil {
				return fmt.Errorf("resolve step %q env: %w", expanded.Steps[i].ID, err)
			}
		}
		return runDryRunReport(expanded, dagPath)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return executeRun(ctx, dagPath, params)
}

// executeRun loads and runs a DAG, honoring the given context for cancellation.
// Shared by `daggle run` and `daggle watch`.
func executeRun(ctx context.Context, dagPath string, params map[string]string) error {
	expanded, err := dag.LoadAndExpand(dagPath, params)
	if err != nil {
		return fmt.Errorf("DAG at %s: %w", dagPath, err)
	}

	if err := dag.ResolveEnv(expanded.Env); err != nil {
		return fmt.Errorf("resolve env: %w", err)
	}
	for i := range expanded.Steps {
		if err := dag.ResolveEnv(expanded.Steps[i].Env); err != nil {
			return fmt.Errorf("resolve step %q env: %w", expanded.Steps[i].ID, err)
		}
	}

	redactor := dag.NewRedactor(dag.AllEnvMaps(expanded)...)

	priorRun, _ := state.LatestRun(expanded.Name)

	run, err := state.CreateRun(expanded.Name)
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}

	fmt.Printf("Starting DAG %q (run %s)\n", expanded.Name, run.ID)
	fmt.Printf("Run directory: %s\n\n", run.Dir)

	if err := copyFile(dagPath, filepath.Join(run.Dir, state.DAGYAMLName)); err != nil {
		fmt.Printf("Warning: failed to snapshot DAG YAML: %v\n", err)
	}

	if priorRun != nil {
		priorMeta, err := state.ReadMeta(priorRun.Dir)
		if err == nil && priorMeta.DAGHash != "" {
			currentHash, _ := dag.HashFile(dagPath)
			if currentHash != "" && currentHash != priorMeta.DAGHash {
				if wrote, err := state.WriteDAGDiff(run.Dir, priorRun); err != nil {
					fmt.Printf("Warning: failed to write dag_diff.patch: %v\n", err)
				} else if wrote {
					fmt.Printf("DAG changed since prior run %s — diff written to %s\n",
						priorRun.ID, state.DAGDiffName)
				}
			}
		}
	}

	if expanded.RVersion != "" {
		rVersion := detectRVersion()
		msg, ok := dag.CheckRVersion(expanded.RVersion, rVersion)
		if !ok {
			if expanded.RVersionStrict {
				return fmt.Errorf("r_version check failed: %s", msg)
			}
			fmt.Printf("Warning: %s\n", msg)
		}
	}

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

	projectDir := resolveProjectDir(expanded)
	renvInfo := renv.Detect(projectDir, rVersion, rPlatform)
	if renvInfo.Detected {
		meta.RenvDetected = true
		meta.RenvLibrary = renvInfo.LibraryPath
		renvLockPath := filepath.Join(projectDir, "renv.lock")
		if renvLockHash, err := dag.HashFile(renvLockPath); err == nil {
			meta.RenvLockHash = renvLockHash
		}
		if renvInfo.LibraryReady {
			fmt.Printf("Detected renv.lock — using library: %s\n", renvInfo.LibraryPath)
		} else {
			fmt.Printf("Warning: renv.lock found but library directory does not exist: %s\n", renvInfo.LibraryPath)
			fmt.Printf("  Run renv::restore() to install packages.\n")
		}
	}

	cacheDir := filepath.Join(state.DataDir(), "cache")
	cfg := engine.Config{
		DAG:           expanded,
		Run:           run,
		ExecFactory:   executor.New,
		Meta:          meta,
		Redactor:      redactor,
		CacheStore:    cache.NewStore(cacheDir),
		Notifications: globalCfg.Notifications,
	}
	if renvInfo.Detected && renvInfo.LibraryReady {
		cfg.RenvLibPath = renvInfo.LibraryPath
	}
	eng, err := engine.New(cfg)
	if err != nil {
		return fmt.Errorf("engine init: %w", err)
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
	// Search across all DAG sources
	for _, src := range state.BuildDAGSources() {
		for _, ext := range []string{".yaml", ".yml"} {
			p := filepath.Join(src.Dir, name+ext)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	// Fall back to current DAGDir for error messaging
	return filepath.Join(state.DAGDir(), name+".yaml")
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
	out, err := exec.Command(state.ToolPath("rscript"), "--version").CombinedOutput()
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

// copyFile copies src to dst preserving the source mode.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}
