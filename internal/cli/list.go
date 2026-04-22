package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var (
	listTagFilter   string
	listTeamFilter  string
	listOwnerFilter string
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available DAGs",
	Args:  cobra.NoArgs,
	RunE:  listDAGs,
}

func init() {
	listCmd.Flags().StringVar(&listTagFilter, "tag", "", "filter by tag")
	listCmd.Flags().StringVar(&listTeamFilter, "team", "", "filter by team")
	listCmd.Flags().StringVar(&listOwnerFilter, "owner", "", "filter by owner")
	rootCmd.AddCommand(listCmd)
}

func listDAGs(_ *cobra.Command, _ []string) error {
	applyOverrides()
	sources := state.BuildDAGSources()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tPROJECT\tOWNER\tTEAM\tTAGS\tSTEPS\tLAST RUN\tSTATUS")

	found := false
	_ = state.WalkDAGFiles(sources, func(src state.DAGSource, path string) error {
		d, err := dag.ParseFile(path)
		if err != nil {
			// Can't filter an invalid DAG by owner/team/tag; hide from filtered views.
			if listTagFilter != "" || listTeamFilter != "" || listOwnerFilter != "" {
				return nil
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t-\t-\t-\t-\t-\tINVALID\n", state.DAGNameFromFile(path), src.Name)
			found = true
			return nil
		}

		filters := dag.Filters{Tag: listTagFilter, Team: listTeamFilter, Owner: listOwnerFilter}
		if !filters.Match(d) {
			return nil
		}

		lastRun := "-"
		status := "-"
		if run, err := state.LatestRun(d.Name); err == nil && run != nil {
			lastRun = run.StartTime.Format("2006-01-02 15:04")
			status = state.RunStatus(run.Dir)
		}

		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			d.Name, src.Name,
			dashIfEmpty(d.Owner),
			dashIfEmpty(d.Team),
			dashIfEmpty(strings.Join(d.Tags, ",")),
			len(d.Steps), lastRun, status,
		)
		found = true
		return nil
	})
	_ = w.Flush()

	if !found {
		fmt.Println("No DAGs found. Create DAG files or register a project with `daggle register`.")
	}

	return nil
}
