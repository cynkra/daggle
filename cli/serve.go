package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/cynkra/daggle/scheduler"
	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the scheduler daemon",
	Long:  "Start the cron scheduler that monitors DAG files and triggers runs on their defined schedules.",
	Args:  cobra.NoArgs,
	RunE:  serveDaemon,
}

func init() {
	rootCmd.AddCommand(serveCmd)
}

func serveDaemon(_ *cobra.Command, _ []string) error {
	applyOverrides()
	dagDir := state.DAGDir()

	// Check if another scheduler is already running
	if scheduler.IsRunning() {
		pid, _ := scheduler.ReadPID()
		return fmt.Errorf("scheduler already running (PID %d). Stop it first or remove %s", pid, scheduler.PIDPath())
	}

	// Write PID file
	if err := scheduler.WritePID(); err != nil {
		return fmt.Errorf("write PID file: %w", err)
	}
	defer func() { _ = scheduler.RemovePID() }()

	fmt.Printf("Starting scheduler\n")
	fmt.Printf("DAG directory: %s\n", dagDir)
	fmt.Printf("PID file: %s\n\n", scheduler.PIDPath())

	// Set up signal handling: SIGINT/SIGTERM for shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// SIGHUP for immediate DAG reload
	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)

	sched := scheduler.New(dagDir)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-sighup:
				fmt.Println("SIGHUP received, reloading DAGs...")
				sched.Reload(ctx)
			}
		}
	}()

	return sched.Start(ctx)
}
