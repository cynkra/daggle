package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/cynkra/daggle/scheduler"
	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var projectName string

var registerCmd = &cobra.Command{
	Use:   "register [path]",
	Short: "Register a project for scheduled execution",
	Long:  "Add a project directory to the registry so daggle serve picks up its .daggle/ DAGs.",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runRegister,
}

func init() {
	registerCmd.Flags().StringVar(&projectName, "name", "", "project name (default: directory basename)")
	rootCmd.AddCommand(registerCmd)
}

func runRegister(_ *cobra.Command, args []string) error {
	path := "."
	if len(args) > 0 {
		path = args[0]
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	name := projectName
	if name == "" {
		name = filepath.Base(absPath)
	}

	// Warn if .daggle/ doesn't exist
	dagDir := filepath.Join(absPath, ".daggle")
	if _, err := os.Stat(dagDir); os.IsNotExist(err) {
		fmt.Printf("Warning: %s does not exist yet. DAGs will be picked up when the directory is created.\n", dagDir)
	}

	// Check for DAG name collisions
	existingSources := state.BuildDAGSources()
	existingNames := state.CollectDAGNames(existingSources)

	newSource := state.DAGSource{Name: name, Dir: dagDir}
	newNames := state.CollectDAGNames([]state.DAGSource{newSource})

	for dagName := range newNames {
		if owner, exists := existingNames[dagName]; exists {
			return fmt.Errorf("DAG %q already exists in project %q — cannot register %q", dagName, owner, name)
		}
	}

	if err := state.RegisterProject(name, absPath); err != nil {
		return err
	}

	fmt.Printf("Registered project %q (%s)\n", name, absPath)

	// Notify running scheduler
	notifyScheduler()

	return nil
}

var unregisterCmd = &cobra.Command{
	Use:   "unregister <name|path>",
	Short: "Remove a project from the registry",
	Args:  cobra.ExactArgs(1),
	RunE:  runUnregister,
}

func init() {
	rootCmd.AddCommand(unregisterCmd)
}

func runUnregister(_ *cobra.Command, args []string) error {
	if err := state.UnregisterProject(args[0]); err != nil {
		return err
	}

	fmt.Printf("Unregistered project %q\n", args[0])
	notifyScheduler()
	return nil
}

func notifyScheduler() {
	if !scheduler.IsRunning() {
		return
	}
	pid, err := scheduler.ReadPID()
	if err != nil {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	if err := proc.Signal(syscall.SIGHUP); err == nil {
		fmt.Println("Scheduler notified to reload.")
	}
}

// dagNamesInDir returns DAG names found in a directory.
func dagNamesInDir(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasSuffix(n, ".yaml") && !strings.HasSuffix(n, ".yml") {
			continue
		}
		if n == "base.yaml" || n == "base.yml" {
			continue
		}
		names = append(names, strings.TrimSuffix(strings.TrimSuffix(n, ".yaml"), ".yml"))
	}
	return names
}
