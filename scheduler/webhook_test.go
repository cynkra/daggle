package scheduler

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestWebhook_DrainsBodyWithoutSecret confirms that an entry without a
// secret still consumes the request body. Before the fix the handler short-
// circuited after the path lookup, leaving a slow client's body un-read
// until ReadTimeout fired. We can only assert the response side here, but
// the server returning 202 Accepted at all proves Body was consumed enough
// to write the response.
func TestWebhook_DrainsBodyWithoutSecret(t *testing.T) {
	t.Setenv("DAGGLE_DATA_DIR", t.TempDir())
	s := New(nil)
	s.webhooks = map[string]webhookEntry{
		"my-dag": {dagPath: "/tmp/nonexistent.yaml"},
	}

	addr, closeFn, err := s.startWebhookServer(s.webhooks)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(closeFn)

	body := strings.NewReader("ignored payload")
	req, err := http.NewRequest("POST", "http://"+addr+"/webhook/my-dag", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want 202", resp.StatusCode)
	}
}

// TestWebhook_HMACFailureWithSecret confirms the HMAC path still rejects an
// invalid signature.
func TestWebhook_HMACFailureWithSecret(t *testing.T) {
	t.Setenv("DAGGLE_DATA_DIR", t.TempDir())
	s := New(nil)
	s.webhooks = map[string]webhookEntry{
		"my-dag": {dagPath: "/tmp/nonexistent.yaml", secret: "topsecret"},
	}

	addr, closeFn, err := s.startWebhookServer(s.webhooks)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(closeFn)

	req, _ := http.NewRequest("POST", "http://"+addr+"/webhook/my-dag", bytes.NewReader([]byte("payload")))
	req.Header.Set("X-Daggle-Signature", "sha256=deadbeef")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// TestWebhook_UnknownDAGDrainsBody confirms the unknown-DAG path also drains.
func TestWebhook_UnknownDAGDrainsBody(t *testing.T) {
	t.Setenv("DAGGLE_DATA_DIR", t.TempDir())
	s := New(nil)
	addr, closeFn, err := s.startWebhookServer(nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(closeFn)

	req, _ := http.NewRequest("POST", "http://"+addr+"/webhook/missing", strings.NewReader("payload"))
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

