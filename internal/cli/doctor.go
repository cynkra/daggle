package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/renv"
	"github.com/cynkra/daggle/scheduler"
	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check system health and configuration",
	Long:  "Run diagnostic checks for R, renv, scheduler, DAGs, and configuration paths.",
	Args:  cobra.NoArgs,
	RunE:  runDoctor,
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}

func runDoctor(_ *cobra.Command, _ []string) error {
	applyOverrides()

	fmt.Println("daggle doctor")
	fmt.Println("=============")
	fmt.Println()

	// Tools
	tools := state.ResolvedTools()
	for name, path := range tools {
		if name == path {
			printCheck("WARN", "%s: not found (using bare name %q)", name, path)
		} else {
			printCheck("OK", "%s: %s", name, path)
		}
	}

	fmt.Println()

	// R version
	rVersion := detectRVersion()
	if rVersion != "" {
		printCheck("OK", "R version: %s", rVersion)
	} else {
		printCheck("FAIL", "Rscript not found in PATH")
	}

	// R platform
	rPlatform := renv.DetectRPlatform()
	if rPlatform != "" {
		printCheck("OK", "R platform: %s", rPlatform)
	} else if rVersion != "" {
		printCheck("WARN", "Could not detect R platform")
	}

	// renv
	cwd, _ := os.Getwd()
	renvInfo := renv.Detect(cwd, rVersion, rPlatform)
	if renvInfo.Detected {
		if renvInfo.LibraryReady {
			printCheck("OK", "renv detected: %s", renvInfo.LibraryPath)
		} else {
			printCheck("WARN", "renv.lock found but library missing: %s", renvInfo.LibraryPath)
			fmt.Printf("         Run renv::restore() to install packages.\n")
		}
	} else {
		printCheck("INFO", "No renv.lock in current directory")
	}

	fmt.Println()

	// Scheduler
	if scheduler.IsRunning() {
		pid, _ := scheduler.ReadPID()
		printCheck("OK", "Scheduler running (PID %d)", pid)
	} else {
		printCheck("INFO", "Scheduler not running")
	}

	fmt.Println()

	// Paths & DAG sources
	dataDir := state.DataDir()
	printCheck("INFO", "Data directory: %s", dataDir)

	sources := state.BuildDAGSources()
	printCheck("INFO", "DAG sources: %d", len(sources))

	fmt.Println()

	// Registered projects
	projects, _ := state.LoadProjects()
	if len(projects) > 0 {
		printCheck("INFO", "Registered projects: %d", len(projects))
		for _, p := range projects {
			dagDir := filepath.Join(p.Path, ".daggle")
			if _, err := os.Stat(dagDir); os.IsNotExist(err) {
				printCheck("WARN", "  %s: .daggle/ missing (%s)", p.Name, p.Path)
			} else {
				printCheck("OK", "  %s: %s", p.Name, p.Path)
			}
		}
	} else {
		printCheck("INFO", "No registered projects")
	}

	fmt.Println()

	// DAG files across all sources
	var totalValid, totalInvalid int
	var allParseErrors []string

	for _, src := range sources {
		entries, err := os.ReadDir(src.Dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			printCheck("WARN", "Cannot read %s (%s): %v", src.Name, src.Dir, err)
			continue
		}

		var valid, invalid int
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || (!strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml")) {
				continue
			}
			if name == "base.yaml" || name == "base.yml" {
				continue
			}
			path := filepath.Join(src.Dir, name)
			if _, err := dag.ParseFile(path); err != nil {
				invalid++
				allParseErrors = append(allParseErrors, fmt.Sprintf("  %s/%s: %v", src.Name, name, err))
			} else {
				valid++
			}
		}
		totalValid += valid
		totalInvalid += invalid
	}

	if totalValid+totalInvalid == 0 {
		printCheck("INFO", "No DAG files found")
		fmt.Println("         Create DAGs with: daggle init <template>")
		fmt.Println("         Or register a project: daggle register")
	} else {
		printCheck("OK", "%d DAG(s) found, %d valid", totalValid+totalInvalid, totalValid)
		if totalInvalid > 0 {
			printCheck("WARN", "%d DAG(s) have errors:", totalInvalid)
			for _, e := range allParseErrors {
				fmt.Println(e)
			}
		}
	}

	// Check for name collisions
	names := state.CollectDAGNames(sources)
	// CollectDAGNames returns first-seen only, so check for duplicates manually
	seen := make(map[string]string)
	for _, src := range sources {
		for _, n := range dagNamesInDir(src.Dir) {
			if prev, exists := seen[n]; exists && prev != src.Name {
				printCheck("WARN", "DAG name collision: %q exists in both %q and %q", n, prev, src.Name)
			} else {
				seen[n] = src.Name
			}
		}
	}
	_ = names // used for the collision check pattern

	fmt.Println()
	return nil
}

func printCheck(level, format string, args ...any) {
	prefix := "  "
	switch level {
	case "OK":
		prefix = "  [OK]   "
	case "WARN":
		prefix = "  [WARN] "
	case "FAIL":
		prefix = "  [FAIL] "
	case "INFO":
		prefix = "  [INFO] "
	}
	fmt.Printf(prefix+format+"\n", args...)
}
