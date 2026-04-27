package cli

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

const whyStderrLines = 20

var whyCmd = &cobra.Command{
	Use:   "why <dag> [run-id]",
	Short: "Diagnose why a run failed",
	Long: "Show the failed step, its error, last stderr lines, upstream status, " +
		"freshness state, and DAG hash change since the last successful run.\n\n" +
		"If run-id is omitted, uses the most recent failed run.",
	Args: cobra.RangeArgs(1, 2),
	RunE: whyRun,
}

func init() {
	rootCmd.AddCommand(whyCmd)
}

func whyRun(_ *cobra.Command, args []string) error {
	dagName := args[0]
	applyOverrides()

	var run *state.RunInfo
	var err error
	if len(args) == 2 && args[1] != "" && args[1] != "latest" {
		run, err = state.FindRun(dagName, args[1])
	} else {
		run, err = state.LatestRunWithStatus(dagName, "failed")
		if err == nil && run == nil {
			return fmt.Errorf("no failed runs found for DAG %q", dagName)
		}
	}
	if err != nil {
		return err
	}

	events, err := state.ReadEvents(run.Dir)
	if err != nil {
		return fmt.Errorf("read events: %w", err)
	}

	// Build a redactor so resolved-secret values don't leak through error
	// strings or stderr tails. Prefer the path the run was launched from
	// (recorded in meta) so we redact against the same env it used; fall
	// back to resolveDAGPath if meta is missing or stale.
	redactor := buildWhyRedactor(dagName, run.Dir)

	fmt.Printf("DAG: %s\n", dagName)
	fmt.Printf("Run: %s\n", run.ID)
	fmt.Printf("Started: %s\n", run.StartTime.Format("2006-01-02 15:04:05"))
	fmt.Printf("Status: %s\n\n", state.RunStatus(run.Dir))

	summaries := state.BuildStepSummaries(events)
	failed := firstFailedStep(summaries)
	if failed == nil {
		fmt.Println("No failed steps found in this run. Nothing to explain.")
		return nil
	}

	fmt.Printf("Failed step: %s\n", failed.StepID)
	if failed.Error != "" {
		fmt.Printf("Error: %s\n", redactor.Redact(failed.Error))
	}
	if failed.ErrorDetail != "" {
		fmt.Printf("Error detail:\n  %s\n", strings.ReplaceAll(redactor.Redact(failed.ErrorDetail), "\n", "\n  "))
	}

	stderr := tailStderr(run.Dir, failed.StepID, whyStderrLines)
	if stderr != "" {
		fmt.Printf("\nLast %d lines of stderr:\n%s\n", whyStderrLines, indent(redactor.Redact(stderr), "  "))
	}

	// Upstream step states
	fmt.Println("\nStep states:")
	for _, ss := range summaries {
		marker := "  "
		if ss.StepID == failed.StepID {
			marker = "* "
		}
		dur := ""
		if ss.Duration > 0 {
			dur = "  " + ss.Duration.String()
		}
		fmt.Printf("%s%-20s %s%s\n", marker, ss.StepID, ss.Status, dur)
	}

	// Freshness events
	freshness := freshnessEvents(events)
	if len(freshness) > 0 {
		fmt.Println("\nFreshness checks:")
		for _, fe := range freshness {
			fmt.Printf("  %s\n", fe)
		}
	}

	// DAG hash diff vs. last successful run
	meta, _ := state.ReadMeta(run.Dir)
	if meta != nil && meta.DAGHash != "" {
		prev, err := state.LatestRunWithStatus(dagName, "completed")
		if err == nil && prev != nil {
			prevMeta, _ := state.ReadMeta(prev.Dir)
			switch {
			case prevMeta == nil || prevMeta.DAGHash == "":
				fmt.Printf("\nDAG hash: %s (no hash on last successful run %s)\n", meta.DAGHash[:12], prev.ID)
			case prevMeta.DAGHash == meta.DAGHash:
				fmt.Printf("\nDAG hash unchanged since last successful run %s.\n", prev.ID)
			default:
				fmt.Printf("\nDAG hash changed since last successful run %s:\n  previous: %s\n  current:  %s\n",
					prev.ID, prevMeta.DAGHash[:12], meta.DAGHash[:12])
			}
		} else {
			fmt.Printf("\nDAG hash: %s (no previous successful run to compare)\n", meta.DAGHash[:12])
		}
	}

	return nil
}

// buildWhyRedactor returns a Redactor for the given DAG. It tries the
// DAGPath recorded in run metadata first (so we redact against the env the
// run actually used), then falls back to the current path resolved by name.
// On any failure to load/resolve, returns a no-op Redactor so `why` still
// prints — secrets that can't be re-resolved simply pass through.
func buildWhyRedactor(dagName, runDir string) *dag.Redactor {
	var path string
	if meta, _ := state.ReadMeta(runDir); meta != nil && meta.DAGPath != "" {
		if _, err := os.Stat(meta.DAGPath); err == nil {
			path = meta.DAGPath
		}
	}
	if path == "" {
		path = resolveDAGPath(dagName)
	}
	r, err := dag.LoadRedactor(path)
	if err != nil {
		slog.Debug("redactor load failed in why; output may include unredacted secrets", "dag", dagName, "error", err)
	}
	return r
}

func firstFailedStep(summaries []state.StepState) *state.StepState {
	for i := range summaries {
		if summaries[i].Status == "failed" {
			return &summaries[i]
		}
	}
	return nil
}

// tailStderr returns the last n lines of a step's stderr log, or "" if missing.
func tailStderr(runDir, stepID string, n int) string {
	path := filepath.Join(runDir, stepID+".stderr.log")
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return strings.Join(lines, "\n")
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// freshnessEvents extracts human-readable freshness check messages.
// Freshness checks surface via step_failed / step_completed with Message
// or via a step that uses the validate/freshness protocol.
func freshnessEvents(events []state.Event) []string {
	var out []string
	for _, e := range events {
		if e.Message == "" {
			continue
		}
		if strings.Contains(strings.ToLower(e.Message), "fresh") ||
			strings.Contains(strings.ToLower(e.Message), "stale") {
			out = append(out, fmt.Sprintf("[%s] %s", e.StepID, e.Message))
		}
	}
	return out
}
