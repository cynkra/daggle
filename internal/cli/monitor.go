package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/cynkra/daggle/api"
	"github.com/cynkra/daggle/internal/tui"
	"github.com/cynkra/daggle/state"
)

var (
	monitorRunID string
	monitorURL   string
)

var monitorCmd = &cobra.Command{
	Use:   "monitor <dag>",
	Short: "Live terminal dashboard for a DAG run",
	Long: "Streams events from /api/v1/dags/{dag}/runs/{run-id}/stream via SSE " +
		"and renders them as a live table. If --url is not given and no local " +
		"server is running, starts a temporary read-only server on a random port " +
		"for the session.",
	Args: cobra.ExactArgs(1),
	RunE: runMonitor,
}

func init() {
	monitorCmd.Flags().StringVar(&monitorRunID, "run-id", "latest", "run ID to monitor (default: latest)")
	monitorCmd.Flags().StringVar(&monitorURL, "url", "", "daggle API base URL (default: http://localhost:8080 or start an embedded server)")
	rootCmd.AddCommand(monitorCmd)
}

func runMonitor(_ *cobra.Command, args []string) error {
	dagName := args[0]
	applyOverrides()

	// Resolve run ID once so the TUI can display it immediately.
	run, err := state.FindRun(dagName, monitorRunID)
	if err != nil {
		return err
	}

	baseURL, stop, err := resolveAPIBaseURL(monitorURL)
	if err != nil {
		return err
	}
	if stop != nil {
		defer stop()
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	return tui.Run(ctx, tui.Config{
		BaseURL: baseURL,
		DAG:     dagName,
		RunID:   run.ID,
	})
}

// resolveAPIBaseURL returns a base URL for the daggle HTTP API.
// If forceURL is non-empty, it is returned verbatim.
// Otherwise, tries http://localhost:8080; if nothing is listening, starts a
// new read-only API server on a random free port and returns that.
// The stop function (possibly nil) should be called when the caller is done.
func resolveAPIBaseURL(forceURL string) (string, func(), error) {
	if forceURL != "" {
		return strings.TrimRight(forceURL, "/"), nil, nil
	}

	defaultURL := "http://localhost:8080"
	if pingAPI(defaultURL) {
		return defaultURL, nil, nil
	}

	// Start an embedded server on a random port so the TUI has something to stream from.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("listen: %w", err)
	}
	sources := state.BuildDAGSources()
	srv := api.New(func() []state.DAGSource { return sources }, Version)
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = httpSrv.Serve(ln) }()
	addr := fmt.Sprintf("http://%s", ln.Addr().String())
	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
		srv.Shutdown()
	}
	return addr, stop, nil
}

func pingAPI(base string) bool {
	client := &http.Client{Timeout: 300 * time.Millisecond}
	resp, err := client.Get(base + "/api/v1/health")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
