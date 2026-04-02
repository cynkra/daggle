package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available DAGs",
	Args:  cobra.NoArgs,
	RunE:  listDAGs,
}

func init() {
	rootCmd.AddCommand(listCmd)
}

func listDAGs(_ *cobra.Command, _ []string) error {
	applyOverrides()
	dir := state.DAGDir()

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("No DAGs directory found at %s\n", dir)
			fmt.Println("Create DAG files in this directory to get started.")
			return nil
		}
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tSTEPS\tLAST RUN\tSTATUS")

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}

		dagPath := filepath.Join(dir, name)
		d, err := dag.ParseFile(dagPath)
		if err != nil {
			dagName := strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
			_, _ = fmt.Fprintf(w, "%s\t-\t-\tINVALID\n", dagName)
			continue
		}

		lastRun := "-"
		status := "-"
		if run, err := state.LatestRun(d.Name); err == nil && run != nil {
			lastRun = run.StartTime.Format("2006-01-02 15:04")
			status = state.RunStatus(run.Dir)
		}

		_, _ = fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", d.Name, len(d.Steps), lastRun, status)
	}
	_ = w.Flush()

	return nil
}
