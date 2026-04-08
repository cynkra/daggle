package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/cynkra/daggle/api"
	"github.com/cynkra/daggle/scheduler"
	"github.com/cynkra/daggle/state"
	"github.com/spf13/cobra"
)

var apiPort int

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the scheduler daemon and REST API",
	Long:  "Start the cron scheduler that monitors DAG files and triggers runs on their defined schedules. Optionally starts the REST API server.",
	Args:  cobra.NoArgs,
	RunE:  serveDaemon,
}

func init() {
	serveCmd.Flags().IntVar(&apiPort, "port", 0, "start REST API on this port (e.g. 8787)")
	rootCmd.AddCommand(serveCmd)
}

func serveDaemon(_ *cobra.Command, _ []string) error {
	applyOverrides()
	sources := buildSchedulerSources()

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
	fmt.Printf("DAG sources: %d\n", len(sources))
	for _, src := range sources {
		fmt.Printf("  %s: %s\n", src.Name, src.Dir)
	}
	fmt.Printf("PID file: %s\n", scheduler.PIDPath())

	// Set up signal handling: SIGINT/SIGTERM for shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// SIGHUP for immediate DAG reload
	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)

	sched := scheduler.New(sources)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-sighup:
				fmt.Println("SIGHUP received, reloading DAGs...")
				sched.Reload(ctx, buildSchedulerSources())
			}
		}
	}()

	// Start REST API server if port is specified
	if apiPort > 0 {
		apiServer := api.New(buildAPISources, Version)
		addr := fmt.Sprintf("127.0.0.1:%d", apiPort)
		httpServer := &http.Server{
			Addr:    addr,
			Handler: apiServer.Handler(),
		}

		go func() {
			fmt.Printf("REST API: http://%s/api/v1\n", addr)
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				fmt.Printf("API server error: %v\n", err)
			}
		}()

		go func() {
			<-ctx.Done()
			_ = httpServer.Close()
		}()
	}

	fmt.Println()
	return sched.Start(ctx)
}

func buildSchedulerSources() []scheduler.DAGSource {
	stateSources := state.BuildDAGSources()
	sources := make([]scheduler.DAGSource, len(stateSources))
	for i, s := range stateSources {
		sources[i] = scheduler.DAGSource{Name: s.Name, Dir: s.Dir}
	}
	return sources
}

func buildAPISources() []api.DAGSource {
	stateSources := state.BuildDAGSources()
	sources := make([]api.DAGSource, len(stateSources))
	for i, s := range stateSources {
		sources[i] = api.DAGSource{Name: s.Name, Dir: s.Dir}
	}
	return sources
}
