package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "List registered projects",
	Args:  cobra.NoArgs,
	RunE:  runProjects,
}

func init() {
	rootCmd.AddCommand(projectsCmd)
}

func runProjects(_ *cobra.Command, _ []string) error {
	projects, err := state.LoadProjects()
	if err != nil {
		return err
	}

	// Show global dags dir
	globalDir := filepath.Join(state.ConfigDir(), "dags")
	globalCount := len(dagNamesInDir(globalDir))
	exists := "ok"
	if _, err := os.Stat(globalDir); os.IsNotExist(err) {
		exists = "missing"
	}
	fmt.Printf("%-20s %-8s %3d DAGs  %s\n", "(global)", exists, globalCount, globalDir)

	if len(projects) == 0 {
		fmt.Println("\nNo projects registered. Use `daggle register` from a project directory.")
		return nil
	}

	for _, p := range projects {
		dagDir := filepath.Join(p.Path, ".daggle")
		count := len(dagNamesInDir(dagDir))
		status := "ok"
		if _, err := os.Stat(dagDir); os.IsNotExist(err) {
			status = "missing"
		}
		fmt.Printf("%-20s %-8s %3d DAGs  %s\n", p.Name, status, count, p.Path)
	}

	return nil
}
