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

	// Paths
	dagDir := state.DAGDir()
	dataDir := state.DataDir()
	printCheck("INFO", "DAG directory: %s", dagDir)
	printCheck("INFO", "Data directory: %s", dataDir)

	fmt.Println()

	// DAG files
	entries, err := os.ReadDir(dagDir)
	if err != nil {
		printCheck("WARN", "Cannot read DAG directory: %v", err)
	} else {
		var valid, invalid int
		var parseErrors []string
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || (!strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml")) {
				continue
			}
			path := filepath.Join(dagDir, name)
			if _, err := dag.ParseFile(path); err != nil {
				invalid++
				parseErrors = append(parseErrors, fmt.Sprintf("  %s: %v", name, err))
			} else {
				valid++
			}
		}
		if valid+invalid == 0 {
			printCheck("INFO", "No DAG files found")
		} else {
			printCheck("OK", "%d DAG(s) found, %d valid", valid+invalid, valid)
			if invalid > 0 {
				printCheck("WARN", "%d DAG(s) have errors:", invalid)
				for _, e := range parseErrors {
					fmt.Println(e)
				}
			}
		}
	}

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
