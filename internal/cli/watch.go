package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/cynkra/daggle/dag"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

var (
	watchParams   []string
	watchDebounce string
)

var watchCmd = &cobra.Command{
	Use:   "watch <dag-name>",
	Short: "Watch a DAG and referenced scripts; re-run on change",
	Long: `Monitor a DAG's YAML file and the scripts it references for changes.
On save, re-validate the DAG and run it. Steps with 'cache: true' skip
automatically when their inputs haven't changed, so only the affected
steps re-execute.

Press Ctrl+C to stop watching.`,
	Args: cobra.ExactArgs(1),
	RunE: runWatch,
}

func init() {
	watchCmd.Flags().StringArrayVarP(&watchParams, "param", "p", nil, "parameter override (key=value)")
	watchCmd.Flags().StringVar(&watchDebounce, "debounce", "500ms", "delay after last change before re-running")
	rootCmd.AddCommand(watchCmd)
}

func runWatch(_ *cobra.Command, args []string) error {
	dagName := args[0]
	applyOverrides()

	dagPath := resolveDAGPath(dagName)
	params := parseParams(watchParams)

	debounce, err := time.ParseDuration(watchDebounce)
	if err != nil {
		return fmt.Errorf("invalid --debounce: %w", err)
	}

	rootCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Initial run
	fmt.Printf("Watching DAG %q — Ctrl+C to stop\n\n", dagName)
	if err := runOnce(rootCtx, dagPath, params); err != nil && rootCtx.Err() == nil {
		fmt.Printf("Initial run failed: %v\n", err)
	}

	// Loop: re-collect watched paths (they may change with the DAG) and
	// block until fsnotify fires, debounce, then re-run.
	for rootCtx.Err() == nil {
		paths, err := collectWatchPaths(dagPath)
		if err != nil {
			// DAG couldn't be parsed — still watch the YAML so the user can fix it.
			fmt.Printf("Parse error (watching YAML only): %v\n", err)
			paths = []string{dagPath}
		}
		fmt.Printf("\nWatching %d file(s). Save any of them to re-run.\n", len(paths))

		changed, err := waitForChange(rootCtx, paths, debounce)
		if err != nil {
			return err
		}
		if !changed {
			// ctx canceled
			return nil
		}
		fmt.Println()
		if err := runOnce(rootCtx, dagPath, params); err != nil && rootCtx.Err() == nil {
			fmt.Printf("Run failed: %v\n", err)
		}
	}
	return nil
}

// runOnce executes the DAG once, suppressing engine errors in the caller's
// error return so that the watch loop keeps running.
func runOnce(ctx context.Context, dagPath string, params map[string]string) error {
	return executeRun(ctx, dagPath, params)
}

// collectWatchPaths returns the set of absolute file paths whose changes should
// trigger a re-run: the DAG YAML, base.yaml if it exists, and every script
// file referenced by a step (script, validate, quarto, rmd).
func collectWatchPaths(dagPath string) ([]string, error) {
	absDAG, err := filepath.Abs(dagPath)
	if err != nil {
		absDAG = dagPath
	}

	d, err := dag.ParseFile(dagPath)
	if err != nil {
		return []string{absDAG}, err
	}

	seen := map[string]bool{absDAG: true}
	out := []string{absDAG}

	add := func(p string) {
		if p == "" {
			return
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if seen[abs] {
			return
		}
		if _, err := os.Stat(abs); err != nil {
			return
		}
		seen[abs] = true
		out = append(out, abs)
	}

	// base.yaml next to the DAG file
	add(filepath.Join(filepath.Dir(absDAG), "base.yaml"))

	for _, s := range d.Steps {
		workdir := d.ResolveWorkdir(s)
		resolve := func(rel string) string {
			if rel == "" {
				return ""
			}
			if filepath.IsAbs(rel) {
				return rel
			}
			if workdir == "" {
				return rel
			}
			return filepath.Join(workdir, rel)
		}
		add(resolve(s.Script))
		add(resolve(s.Validate))
		add(resolve(s.Quarto))
		add(resolve(s.Rmd))
		if s.Database != nil {
			add(resolve(s.Database.QueryFile))
		}
		if s.Email != nil {
			add(resolve(s.Email.BodyFile))
			for _, a := range s.Email.Attach {
				add(resolve(a))
			}
		}
	}

	sort.Strings(out)
	return out, nil
}

// waitForChange blocks until one of paths is modified (or ctx is canceled),
// then waits for debounce to elapse with no further changes before returning
// true. Returns false if the context is canceled first.
func waitForChange(ctx context.Context, paths []string, debounce time.Duration) (bool, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return false, fmt.Errorf("create watcher: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	// Watch both the files themselves and their parent directories. Editors
	// often replace files on save (rename+create), which fsnotify on the file
	// path misses; the directory watcher catches those.
	dirs := make(map[string]bool)
	for _, p := range paths {
		if err := watcher.Add(p); err != nil {
			// Not fatal — dir watcher will cover it.
			_ = err
		}
		dirs[filepath.Dir(p)] = true
	}
	for d := range dirs {
		_ = watcher.Add(d)
	}

	// Only directory events matching one of our watched paths count.
	watched := make(map[string]bool, len(paths))
	for _, p := range paths {
		watched[p] = true
	}

	var (
		timer *time.Timer
		timeC <-chan time.Time
	)

	for {
		select {
		case <-ctx.Done():
			return false, nil
		case ev, ok := <-watcher.Events:
			if !ok {
				return false, nil
			}
			if !ev.Has(fsnotify.Write) && !ev.Has(fsnotify.Create) && !ev.Has(fsnotify.Rename) {
				continue
			}
			abs, _ := filepath.Abs(ev.Name)
			if !watched[abs] {
				continue
			}
			fmt.Printf("Change detected: %s\n", abs)
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(debounce)
			timeC = timer.C
		case err, ok := <-watcher.Errors:
			if !ok {
				return false, nil
			}
			fmt.Printf("watcher error: %v\n", err)
		case <-timeC:
			return true, nil
		}
	}
}
