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

var (
	apiPort       int
	apiBind       string
	apiBasePath   string
	apiTrustProxy bool
	apiAuthMode   string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the scheduler daemon and REST API",
	Long:  "Start the cron scheduler that monitors DAG files and triggers runs on their defined schedules. Optionally starts the REST API server.",
	Args:  cobra.NoArgs,
	RunE:  serveDaemon,
}

func init() {
	serveCmd.Flags().IntVar(&apiPort, "port", 0, "start REST API on this port (e.g. 8787)")
	serveCmd.Flags().StringVar(&apiBind, "bind", "", "address the API listens on (default 127.0.0.1; 0.0.0.0 requires an auth mode)")
	serveCmd.Flags().StringVar(&apiBasePath, "base-path", "", "mount the API and UI under a sub-path, e.g. /daggle")
	serveCmd.Flags().BoolVar(&apiTrustProxy, "trust-proxy", false, "honour X-Forwarded-Proto/Host/For (only behind a reverse proxy)")
	serveCmd.Flags().StringVar(&apiAuthMode, "auth-mode", "", "authentication mode: none, basic or token")
	rootCmd.AddCommand(serveCmd)
}

// serveSettings resolves the server posture from config.yaml, the environment
// and the flags actually passed, then refuses unsafe combinations.
func serveSettings(cmd *cobra.Command) (serverSettings, error) {
	flags := serveFlags{
		port:          apiPort,
		portSet:       cmd.Flags().Changed("port"),
		bind:          apiBind,
		bindSet:       cmd.Flags().Changed("bind"),
		basePath:      apiBasePath,
		basePathSet:   cmd.Flags().Changed("base-path"),
		trustProxy:    apiTrustProxy,
		trustProxySet: cmd.Flags().Changed("trust-proxy"),
		authMode:      apiAuthMode,
		authModeSet:   cmd.Flags().Changed("auth-mode"),
	}
	settings, err := resolveServerSettings(globalCfg.Server, flags)
	if err != nil {
		return settings, err
	}
	if settings.Auth.Mode == api.AuthModeToken {
		tok, generated, err := ensureToken(settings.Auth.Token)
		if err != nil {
			return settings, err
		}
		settings.Auth.Token = tok
		if generated {
			fmt.Printf("Generated API token: %s\n", tok)
			fmt.Printf("Stored at: %s\n", tokenPath())
		}
	}
	if err := settings.validate(); err != nil {
		return settings, err
	}
	return settings, nil
}

func serveDaemon(cmd *cobra.Command, _ []string) error {
	applyOverrides()

	// Resolve and validate before anything else: an unsafe or misconfigured
	// posture must fail before the PID file is written or a listener opens.
	settings, err := serveSettings(cmd)
	if err != nil {
		return err
	}

	sources := state.BuildDAGSources()

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

	sched := scheduler.NewWithConfig(sources, globalCfg.Scheduler)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-sighup:
				fmt.Println("SIGHUP received, reloading DAGs...")
				sched.Reload(ctx, state.BuildDAGSources())
			}
		}
	}()

	// Start REST API server if a port is configured
	if settings.Port > 0 {
		schedulerStatusFn := func() *api.SchedulerInfo {
			st := sched.Status()
			return &api.SchedulerInfo{
				RegisteredDAGs: st.RegisteredDAGs,
				ActiveRuns:     st.ActiveRuns,
				MaxConcurrent:  st.MaxConcurrent,
				TriggerCounts:  st.TriggerCounts,
			}
		}
		apiServer := api.New(state.BuildDAGSources, Version,
			api.WithSchedulerStatus(schedulerStatusFn),
			api.WithScheduleManager(sched),
			api.WithBasePath(settings.BasePath),
			api.WithTrustProxy(settings.TrustProxy),
			api.WithAuth(settings.Auth),
		)
		addr := settings.Addr()
		httpServer := &http.Server{
			Addr:    addr,
			Handler: apiServer.Handler(),
		}

		go func() {
			fmt.Printf("REST API: http://%s%s/api/v1\n", addr, settings.BasePath)
			fmt.Printf("Auth mode: %s\n", settings.Auth.Mode)
			if settings.TrustProxy {
				fmt.Printf("Trusting X-Forwarded-* headers\n")
			}
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
