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
	sources := state.BuildDAGSources()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tPROJECT\tSTEPS\tLAST RUN\tSTATUS")

	found := false
	for _, src := range sources {
		entries, err := os.ReadDir(src.Dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
				continue
			}
			if name == "base.yaml" || name == "base.yml" {
				continue
			}

			dagPath := filepath.Join(src.Dir, name)
			d, err := dag.ParseFile(dagPath)
			if err != nil {
				dagName := strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
				_, _ = fmt.Fprintf(w, "%s\t%s\t-\t-\tINVALID\n", dagName, src.Name)
				found = true
				continue
			}

			lastRun := "-"
			status := "-"
			if run, err := state.LatestRun(d.Name); err == nil && run != nil {
				lastRun = run.StartTime.Format("2006-01-02 15:04")
				status = state.RunStatus(run.Dir)
			}

			_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", d.Name, src.Name, len(d.Steps), lastRun, status)
			found = true
		}
	}
	_ = w.Flush()

	if !found {
		fmt.Println("No DAGs found. Create DAG files or register a project with `daggle register`.")
	}

	return nil
}
