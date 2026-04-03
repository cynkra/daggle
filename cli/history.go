package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var historyLast int

var historyCmd = &cobra.Command{
	Use:   "history <dag-name>",
	Short: "Show run history for a DAG",
	Args:  cobra.ExactArgs(1),
	RunE:  showHistory,
}

func init() {
	historyCmd.Flags().IntVar(&historyLast, "last", 10, "number of recent runs to show")
	rootCmd.AddCommand(historyCmd)
}

func showHistory(_ *cobra.Command, args []string) error {
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

	if len(runs) > historyLast {
		runs = runs[:historyLast]
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "RUN ID\tSTARTED\tSTATUS\tDAG HASH")

	for _, run := range runs {
		status := state.RunStatus(run.Dir)
		dagHash := ""
		if meta, err := state.ReadMeta(run.Dir); err == nil && meta.DAGHash != "" {
			dagHash = meta.DAGHash[:12]
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			run.ID,
			run.StartTime.Format("2006-01-02 15:04:05"),
			status,
			dagHash,
		)
	}
	_ = w.Flush()
	return nil
}
