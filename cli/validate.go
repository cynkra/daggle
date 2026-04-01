package cli

import (
	"fmt"

	"github.com/schochastics/rdag/dag"
	"github.com/spf13/cobra"
)

var validateCmd = &cobra.Command{
	Use:   "validate <dag-name|path>",
	Short: "Validate a DAG definition",
	Args:  cobra.ExactArgs(1),
	RunE:  validateDAG,
}

func init() {
	rootCmd.AddCommand(validateCmd)
}

func validateDAG(cmd *cobra.Command, args []string) error {
	dagPath := resolveDAGPath(args[0])
	d, err := dag.ParseFile(dagPath)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	// Show execution plan
	tiers, err := dag.TopoSort(d.Steps)
	if err != nil {
		return err
	}

	fmt.Printf("DAG %q is valid\n\n", d.Name)
	fmt.Println("Execution plan:")
	for i, tier := range tiers {
		for _, s := range tier {
			fmt.Printf("  Tier %d: [%s] %s (%s)\n", i, s.ID, stepDescription(s), dag.StepType(s))
		}
	}

	if len(d.Params) > 0 {
		fmt.Println("\nParameters:")
		for _, p := range d.Params {
			def := p.Default
			if def == "" {
				def = "(no default)"
			}
			fmt.Printf("  %s = %s\n", p.Name, def)
		}
	}

	return nil
}

func stepDescription(s dag.Step) string {
	switch {
	case s.Script != "":
		return s.Script
	case s.RExpr != "":
		r := s.RExpr
		if len(r) > 50 {
			r = r[:47] + "..."
		}
		return r
	case s.Command != "":
		c := s.Command
		if len(c) > 50 {
			c = c[:47] + "..."
		}
		return c
	default:
		return ""
	}
}
