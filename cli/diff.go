package cli

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cynkra/daggle/api"
	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var diffCmd = &cobra.Command{
	Use:   "diff <dag-name> <run1> <run2>",
	Short: "Compare outputs, durations, and metadata between two runs",
	Args:  cobra.ExactArgs(3),
	RunE:  runDiff,
}

func init() {
	rootCmd.AddCommand(diffCmd)
}

func runDiff(_ *cobra.Command, args []string) error {
	dagName := args[0]
	run1ID := args[1]
	run2ID := args[2]
	applyOverrides()

	run1, err := findRunByID(dagName, run1ID)
	if err != nil {
		return err
	}
	run2, err := findRunByID(dagName, run2ID)
	if err != nil {
		return err
	}

	cmp := buildComparison(run1, run2)

	printOutputsDiff(cmp.OutputsDiff, run1ID, run2ID)
	printDurationDiff(cmp.DurationDiff, run1ID, run2ID)
	printMetaDiff(cmp.MetaDiff)

	return nil
}

func findRunByID(dagName, runID string) (*state.RunInfo, error) {
	if runID == "latest" {
		run, err := state.LatestRun(dagName)
		if err != nil {
			return nil, err
		}
		if run == nil {
			return nil, fmt.Errorf("no runs found for DAG %q", dagName)
		}
		return run, nil
	}

	runs, err := state.ListRuns(dagName)
	if err != nil {
		return nil, err
	}
	for _, r := range runs {
		if r.ID == runID {
			return &r, nil
		}
	}
	return nil, fmt.Errorf("run %q not found for DAG %q", runID, dagName)
}

func buildComparison(run1, run2 *state.RunInfo) api.CompareResponse {
	var resp api.CompareResponse

	// Collect outputs for both runs.
	out1 := collectOutputs(run1.Dir)
	out2 := collectOutputs(run2.Dir)

	// Build a combined key set.
	allKeys := make(map[diffKey]bool)
	for k := range out1 {
		allKeys[k] = true
	}
	for k := range out2 {
		allKeys[k] = true
	}

	sorted := make([]diffKey, 0, len(allKeys))
	for k := range allKeys {
		sorted = append(sorted, k)
	}
	sortDiffKeys(sorted)

	for _, k := range sorted {
		v1, v2 := out1[k], out2[k]
		if v1 != v2 {
			resp.OutputsDiff = append(resp.OutputsDiff, api.OutputDiff{
				StepID: k.step,
				Key:    k.key,
				Value1: v1,
				Value2: v2,
			})
		}
	}

	// Duration comparison.
	meta1, _ := state.ReadMeta(run1.Dir)
	meta2, _ := state.ReadMeta(run2.Dir)

	if meta1 != nil && meta2 != nil {
		d1 := meta1.EndTime.Sub(meta1.StartTime).Seconds()
		d2 := meta2.EndTime.Sub(meta2.StartTime).Seconds()
		if meta1.EndTime.IsZero() {
			d1 = 0
		}
		if meta2.EndTime.IsZero() {
			d2 = 0
		}
		resp.DurationDiff = api.DurationDiff{
			Run1Seconds: d1,
			Run2Seconds: d2,
			DiffSeconds: d2 - d1,
		}
	}

	// DAG hash comparison.
	hash1, hash2 := "", ""
	if meta1 != nil {
		hash1 = meta1.DAGHash
	}
	if meta2 != nil {
		hash2 = meta2.DAGHash
	}
	resp.MetaDiff = api.MetaDiff{
		DAGHash1: hash1,
		DAGHash2: hash2,
		Changed:  hash1 != hash2,
	}

	return resp
}

type diffKey struct{ step, key string }

func sortDiffKeys(keys []diffKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].step != keys[j].step {
			return keys[i].step < keys[j].step
		}
		return keys[i].key < keys[j].key
	})
}

func collectOutputs(runDir string) map[diffKey]string {
	events, err := state.ReadEvents(runDir)
	if err != nil {
		return nil
	}

	stepIDs := make(map[string]bool)
	for _, e := range events {
		if e.StepID != "" {
			stepIDs[e.StepID] = true
		}
	}

	result := make(map[diffKey]string)
	for stepID := range stepIDs {
		markers := api.ParseOutputMarkers(runDir, stepID)
		for k, v := range markers {
			result[diffKey{step: stepID, key: k}] = v
		}
	}
	return result
}

func printOutputsDiff(diffs []api.OutputDiff, run1ID, run2ID string) {
	if len(diffs) == 0 {
		fmt.Println("No output differences.")
		fmt.Println()
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
	_, _ = fmt.Fprintf(w, "STEP\tOUTPUT\t%s\t%s\tCHANGE\n", strings.ToUpper(run1ID), strings.ToUpper(run2ID))
	for _, d := range diffs {
		change := formatChange(d.Value1, d.Value2)
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", d.StepID, d.Key, d.Value1, d.Value2, change)
	}
	_ = w.Flush()
	fmt.Println()
}

func formatChange(v1, v2 string) string {
	// Try numeric comparison.
	var f1, f2 float64
	n1, err1 := fmt.Sscanf(v1, "%f", &f1)
	n2, err2 := fmt.Sscanf(v2, "%f", &f2)
	if n1 == 1 && n2 == 1 && err1 == nil && err2 == nil {
		diff := f2 - f1
		if diff >= 0 {
			return fmt.Sprintf("+%s", formatNumber(diff))
		}
		return formatNumber(diff)
	}
	return "(changed)"
}

func formatNumber(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return fmt.Sprintf("%d", int64(f))
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%g", f), "0"), ".")
}

func printDurationDiff(d api.DurationDiff, _, _ string) {
	if d.Run1Seconds == 0 && d.Run2Seconds == 0 {
		return
	}
	s1 := fmtSecs(d.Run1Seconds)
	s2 := fmtSecs(d.Run2Seconds)
	diff := fmtSecsDiff(d.DiffSeconds)
	fmt.Printf("Duration: %s -> %s (%s)\n", s1, s2, diff)
}

func printMetaDiff(m api.MetaDiff) {
	if m.DAGHash1 == "" && m.DAGHash2 == "" {
		return
	}
	h1 := truncHash(m.DAGHash1)
	h2 := truncHash(m.DAGHash2)
	label := "unchanged"
	if m.Changed {
		label = "changed"
	}
	fmt.Printf("DAG hash: %s -> %s (%s)\n", h1, h2, label)
}

func truncHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func fmtSecs(secs float64) string {
	d := time.Duration(secs * float64(time.Second))
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func fmtSecsDiff(secs float64) string {
	d := time.Duration(math.Abs(secs) * float64(time.Second))
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	sign := "+"
	if secs < 0 {
		sign = "-"
	}
	if m > 0 {
		return fmt.Sprintf("%s%dm%02ds", sign, m, s)
	}
	return fmt.Sprintf("%s%ds", sign, s)
}
