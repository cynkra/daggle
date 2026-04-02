package cli

import (
	"fmt"
	"syscall"
	"time"

	"github.com/cynkra/daggle/scheduler"
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the scheduler daemon",
	Long:  "Send SIGTERM to the running scheduler daemon for graceful shutdown.",
	Args:  cobra.NoArgs,
	RunE:  stopDaemon,
}

func init() {
	rootCmd.AddCommand(stopCmd)
}

func stopDaemon(_ *cobra.Command, _ []string) error {
	if !scheduler.IsRunning() {
		return fmt.Errorf("scheduler is not running")
	}

	pid, err := scheduler.ReadPID()
	if err != nil {
		return fmt.Errorf("read PID: %w", err)
	}

	fmt.Printf("Stopping scheduler (PID %d)...\n", pid)

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM: %w", err)
	}

	// Wait for process to exit
	for i := 0; i < 30; i++ {
		time.Sleep(200 * time.Millisecond)
		if !scheduler.IsRunning() {
			fmt.Println("Scheduler stopped")
			return nil
		}
	}

	return fmt.Errorf("scheduler did not stop within 6 seconds (PID %d)", pid)
}
