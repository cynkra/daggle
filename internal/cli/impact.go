package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var impactCmd = &cobra.Command{
	Use:   "impact <dag>",
	Short: "Show downstream dependents and declared exposures of a DAG",
	Long: "Lists other DAGs that declare `trigger.on_dag.name: <dag>` plus any " +
		"`exposures:` declared on the DAG itself (Shiny apps, Quarto reports, etc.).",
	Args: cobra.ExactArgs(1),
	RunE: runImpact,
}

func init() {
	rootCmd.AddCommand(impactCmd)
}

func runImpact(_ *cobra.Command, args []string) error {
	target := args[0]
	applyOverrides()
	sources := state.BuildDAGSources()

	// Parse every DAG in every source and collect exposures + downstream refs.
	var targetDAG *dag.DAG
	type downstream struct {
		name    string
		project string
		status  string // "", "completed", "failed", "any"
	}
	var downs []downstream

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
			d, err := dag.ParseFile(filepath.Join(src.Dir, name))
			if err != nil {
				continue
			}
			if d.Name == target {
				targetDAG = d
			}
			if d.Trigger != nil && d.Trigger.OnDAG != nil && d.Trigger.OnDAG.Name == target {
				downs = append(downs, downstream{name: d.Name, project: src.Name, status: d.Trigger.OnDAG.Status})
			}
		}
	}

	if targetDAG == nil {
		return fmt.Errorf("DAG %q not found", target)
	}

	fmt.Printf("DAG: %s\n", target)
	if targetDAG.Description != "" {
		fmt.Printf("Description: %s\n", targetDAG.Description)
	}
	if targetDAG.Owner != "" || targetDAG.Team != "" {
		fmt.Printf("Owner: %s   Team: %s\n", dashIfEmpty(targetDAG.Owner), dashIfEmpty(targetDAG.Team))
	}
	fmt.Println()

	if len(downs) == 0 {
		fmt.Println("Downstream DAGs: (none)")
	} else {
		sort.Slice(downs, func(i, j int) bool { return downs[i].name < downs[j].name })
		fmt.Println("Downstream DAGs:")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "  NAME\tPROJECT\tTRIGGER ON STATUS")
		for _, d := range downs {
			status := d.status
			if status == "" {
				status = "any"
			}
			_, _ = fmt.Fprintf(w, "  %s\t%s\t%s\n", d.name, d.project, status)
		}
		_ = w.Flush()
	}

	fmt.Println()
	if len(targetDAG.Exposures) == 0 {
		fmt.Println("Exposures: (none declared)")
	} else {
		fmt.Println("Exposures:")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "  NAME\tTYPE\tURL\tDESCRIPTION")
		for _, e := range targetDAG.Exposures {
			_, _ = fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n",
				e.Name, e.Type, dashIfEmpty(e.URL), dashIfEmpty(e.Description))
		}
		_ = w.Flush()
	}

	return nil
}
