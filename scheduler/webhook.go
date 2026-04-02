package scheduler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const maxWebhookBodySize = 1 << 20 // 1MB

// webhookEntry tracks a webhook-enabled DAG.
type webhookEntry struct {
	dagPath string
	secret  string
}

// startWebhookServer starts an HTTP server for webhook triggers.
// It blocks until the server is shut down via the returned close function.
func (s *Scheduler) startWebhookServer(_ map[string]webhookEntry) (addr string, closeFn func(), err error) {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /webhook/{dag}", func(w http.ResponseWriter, r *http.Request) {
		dagName := r.PathValue("dag")

		s.mu.Lock()
		entry, ok := s.webhooks[dagName]
		s.mu.Unlock()

		if !ok {
			http.Error(w, "unknown DAG", http.StatusNotFound)
			return
		}

		// Validate HMAC if secret is configured
		if entry.secret != "" {
			body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodySize))
			if err != nil {
				http.Error(w, "failed to read body", http.StatusBadRequest)
				return
			}
			sig := r.Header.Get("X-Daggle-Signature")
			if !validateHMAC(body, sig, entry.secret) {
				http.Error(w, "invalid signature", http.StatusForbidden)
				return
			}
		}

		s.logger.Info("webhook received, triggering run", "dag", dagName)
		s.triggerRun(entry.dagPath, "webhook")

		w.WriteHeader(http.StatusAccepted)
		_, _ = fmt.Fprintf(w, `{"status":"accepted","dag":%q}`, dagName)
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("webhook listen: %w", err)
	}

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			s.logger.Error("webhook server error", "error", err)
		}
	}()

	addr = listener.Addr().String()
	closeFn = func() {
		_ = server.Close()
	}

	return addr, closeFn, nil
}

// validateHMAC checks the HMAC-SHA256 signature of the request body.
// Expected signature format: "sha256=<hex>"
func validateHMAC(body []byte, signature, secret string) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	sigBytes, err := hex.DecodeString(signature[7:])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)
	return hmac.Equal(sigBytes, expected)
}
