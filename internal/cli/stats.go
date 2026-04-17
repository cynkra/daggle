package cli

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var statsLast int

var statsCmd = &cobra.Command{
	Use:   "stats <dag-name>",
	Short: "Show duration trends and success rate from run history",
	Args:  cobra.ExactArgs(1),
	RunE:  showStats,
}

func init() {
	statsCmd.Flags().IntVar(&statsLast, "last", 20, "number of recent runs to analyze")
	rootCmd.AddCommand(statsCmd)
}

func showStats(_ *cobra.Command, args []string) error {
	dagName := args[0]
	applyOverrides()

	runs, err := state.ListRuns(dagName)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		fmt.Printf("No runs found for DAG %q\n", dagName)
		return nil
	}

	// Limit to last N runs
	if len(runs) > statsLast {
		runs = runs[:statsLast]
	}

	var completed, failed int
	stepDurations := make(map[string][]time.Duration)
	stepPeakRSS := make(map[string][]int64)
	stepCPU := make(map[string][]float64)

	for _, run := range runs {
		status := state.RunStatus(run.Dir)
		switch status {
		case "completed":
			completed++
		case "failed":
			failed++
		}

		events, err := state.ReadEvents(run.Dir)
		if err != nil {
			continue
		}
		for _, e := range events {
			if e.StepID == "" || e.Type != state.EventStepCompleted {
				continue
			}
			if e.Duration != "" {
				if d, err := time.ParseDuration(e.Duration); err == nil {
					stepDurations[e.StepID] = append(stepDurations[e.StepID], d)
				}
			}
			if e.PeakRSSKB > 0 {
				stepPeakRSS[e.StepID] = append(stepPeakRSS[e.StepID], e.PeakRSSKB)
			}
			if total := e.UserCPUSec + e.SysCPUSec; total > 0 {
				stepCPU[e.StepID] = append(stepCPU[e.StepID], total)
			}
		}
	}

	total := completed + failed
	fmt.Printf("DAG: %s (%d runs analyzed)\n", dagName, len(runs))
	fmt.Printf("Success rate: ")
	if total > 0 {
		fmt.Printf("%.0f%% (%d/%d)\n", float64(completed)/float64(total)*100, completed, total)
	} else {
		fmt.Printf("N/A (no completed or failed runs)\n")
	}
	fmt.Println()

	if len(stepDurations) == 0 {
		fmt.Println("No step duration data available.")
		return nil
	}

	// Sort step names
	var stepNames []string
	for name := range stepDurations {
		stepNames = append(stepNames, name)
	}
	sort.Strings(stepNames)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "STEP\tRUNS\tAVG\tP50\tP95\tAVG RSS\tP95 RSS\tAVG CPU")
	for _, name := range stepNames {
		durations := stepDurations[name]
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

		avg := averageDuration(durations)
		p50 := percentile(durations, 50)
		p95 := percentile(durations, 95)

		rss := stepPeakRSS[name]
		sort.Slice(rss, func(i, j int) bool { return rss[i] < rss[j] })
		cpu := stepCPU[name]

		_, _ = fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n", name, len(durations),
			formatDuration(avg), formatDuration(p50), formatDuration(p95),
			formatKB(averageInt64(rss)),
			formatKB(percentileInt64(rss, 95)),
			formatCPUSec(averageFloat64(cpu)),
		)
	}
	_ = w.Flush()
	return nil
}

func averageInt64(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	var sum int64
	for _, x := range xs {
		sum += x
	}
	return sum / int64(len(xs))
}

func percentileInt64(xs []int64, p int) int64 {
	if len(xs) == 0 {
		return 0
	}
	idx := (p * len(xs)) / 100
	if idx >= len(xs) {
		idx = len(xs) - 1
	}
	return xs[idx]
}

func averageFloat64(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// formatKB renders a kilobyte count as KB, MB, or GB with one decimal.
func formatKB(kb int64) string {
	if kb <= 0 {
		return "-"
	}
	switch {
	case kb < 1024:
		return fmt.Sprintf("%dKB", kb)
	case kb < 1024*1024:
		return fmt.Sprintf("%.1fMB", float64(kb)/1024)
	default:
		return fmt.Sprintf("%.1fGB", float64(kb)/(1024*1024))
	}
}

// formatCPUSec renders a CPU-seconds value.
func formatCPUSec(sec float64) string {
	if sec <= 0 {
		return "-"
	}
	if sec < 60 {
		return fmt.Sprintf("%.1fs", sec)
	}
	return fmt.Sprintf("%.1fm", sec/60)
}

func averageDuration(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	var sum time.Duration
	for _, d := range ds {
		sum += d
	}
	return sum / time.Duration(len(ds))
}

func percentile(ds []time.Duration, p int) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	idx := (p * len(ds)) / 100
	if idx >= len(ds) {
		idx = len(ds) - 1
	}
	return ds[idx]
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fm", d.Minutes())
}
