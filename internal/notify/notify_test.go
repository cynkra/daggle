package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cynkra/daggle/state"
)

func TestSend_Slack(t *testing.T) {
	var gotBody string
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := state.Config{
		Notifications: map[string]state.NotificationChannel{
			"team": {Type: "slack", WebhookURL: srv.URL},
		},
	}
	if err := Send(cfg, "team", "hello world"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotContentType != "application/json" {
		t.Errorf("content-type = %q, want application/json", gotContentType)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("unmarshal body: %v (body=%q)", err, gotBody)
	}
	if payload["text"] != "hello world" {
		t.Errorf("slack body text = %q, want %q", payload["text"], "hello world")
	}
}

func TestSend_HTTPCustomHeaders(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := state.Config{
		Notifications: map[string]state.NotificationChannel{
			"ops": {
				Type:       "http",
				WebhookURL: srv.URL,
				Headers:    map[string]string{"Authorization": "Bearer secret"},
			},
		},
	}
	if err := Send(cfg, "ops", "boom"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("Authorization header = %q, want Bearer secret", gotAuth)
	}
}

func TestSend_UnknownChannel(t *testing.T) {
	err := Send(state.Config{}, "missing", "x")
	if err == nil {
		t.Fatal("expected error for unknown channel")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error = %v, want contains 'not configured'", err)
	}
}

func TestSend_ServerErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("kaboom"))
	}))
	defer srv.Close()

	cfg := state.Config{
		Notifications: map[string]state.NotificationChannel{
			"team": {Type: "slack", WebhookURL: srv.URL},
		},
	}
	err := Send(cfg, "team", "x")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %v, want status code in message", err)
	}
}

func TestValidateChannel(t *testing.T) {
	cases := []struct {
		name    string
		ch      state.NotificationChannel
		wantErr bool
	}{
		{"slack ok", state.NotificationChannel{Type: "slack", WebhookURL: "http://x"}, false},
		{"slack no url", state.NotificationChannel{Type: "slack"}, true},
		{"clickup ok", state.NotificationChannel{Type: "clickup", WebhookURL: "http://x"}, false},
		{"http ok", state.NotificationChannel{Type: "http", WebhookURL: "http://x"}, false},
		{"smtp ok", state.NotificationChannel{Type: "smtp", SMTPHost: "h", SMTPPort: 25, SMTPFrom: "a@b", SMTPTo: []string{"c@d"}}, false},
		{"smtp missing fields", state.NotificationChannel{Type: "smtp", SMTPHost: "h"}, true},
		{"empty type", state.NotificationChannel{}, true},
		{"unknown type", state.NotificationChannel{Type: "pager"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateChannel(tc.name, tc.ch)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateChannel err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
