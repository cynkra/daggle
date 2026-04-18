// Package notify dispatches notifications through channels defined in config.yaml.
// Channel types: slack, clickup, http (generic webhook), smtp (email).
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"github.com/cynkra/daggle/state"
)

// DefaultTimeout bounds HTTP requests. Exposed for testing.
var DefaultTimeout = 10 * time.Second

// Send dispatches message via the named channel from cfg.
// Returns an error if the channel is unknown or dispatch fails.
func Send(cfg state.Config, channel, message string) error {
	ch, ok := cfg.Notifications[channel]
	if !ok {
		return fmt.Errorf("notification channel %q not configured", channel)
	}
	switch ch.Type {
	case "slack":
		return sendSlack(ch, message)
	case "clickup":
		return sendClickup(ch, message)
	case "http":
		return sendHTTP(ch, message)
	case "smtp":
		return sendSMTP(ch, message)
	default:
		return fmt.Errorf("notification channel %q has unknown type %q", channel, ch.Type)
	}
}

// ValidateChannel checks that a channel config is well-formed.
// Used by config/DAG validators before runtime.
func ValidateChannel(name string, ch state.NotificationChannel) error {
	switch ch.Type {
	case "slack", "clickup":
		if ch.WebhookURL == "" {
			return fmt.Errorf("notifications[%s]: webhook_url is required for type %q", name, ch.Type)
		}
	case "http":
		if ch.WebhookURL == "" {
			return fmt.Errorf("notifications[%s]: webhook_url is required for type \"http\"", name)
		}
	case "smtp":
		if ch.SMTPHost == "" || ch.SMTPPort == 0 || ch.SMTPFrom == "" || len(ch.SMTPTo) == 0 {
			return fmt.Errorf("notifications[%s]: smtp_host, smtp_port, smtp_from, smtp_to are required for type \"smtp\"", name)
		}
	case "":
		return fmt.Errorf("notifications[%s]: type is required", name)
	default:
		return fmt.Errorf("notifications[%s]: unknown type %q", name, ch.Type)
	}
	return nil
}

func sendSlack(ch state.NotificationChannel, message string) error {
	return sendWebhookText(ch.WebhookURL, "text", message, nil)
}

func sendClickup(ch state.NotificationChannel, message string) error {
	return sendWebhookText(ch.WebhookURL, "text", message, nil)
}

// sendWebhookText posts a single-field JSON body (`{bodyKey: message}`) to
// url. Shared by webhook-style channels (slack, clickup) that expect the
// same shape with a different key.
func sendWebhookText(url, bodyKey, message string, headers map[string]string) error {
	body, err := json.Marshal(map[string]string{bodyKey: message})
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}
	return postJSON(url, body, headers)
}

func sendHTTP(ch state.NotificationChannel, message string) error {
	method := ch.Method
	if method == "" {
		method = http.MethodPost
	}
	body, err := json.Marshal(map[string]string{"message": message})
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}
	req, err := http.NewRequest(method, ch.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range ch.Headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: DefaultTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http %s %s: %w", method, ch.WebhookURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return nil
}

func sendSMTP(ch state.NotificationChannel, message string) error {
	addr := fmt.Sprintf("%s:%d", ch.SMTPHost, ch.SMTPPort)
	var auth smtp.Auth
	if ch.SMTPUser != "" {
		auth = smtp.PlainAuth("", ch.SMTPUser, ch.SMTPPassword, ch.SMTPHost)
	}
	subject := "[daggle] notification"
	if i := strings.IndexByte(message, '\n'); i > 0 && i < 120 {
		subject = "[daggle] " + message[:i]
	} else if len(message) < 120 {
		subject = "[daggle] " + message
	}
	msg := []byte(
		"From: " + ch.SMTPFrom + "\r\n" +
			"To: " + strings.Join(ch.SMTPTo, ", ") + "\r\n" +
			"Subject: " + subject + "\r\n" +
			"\r\n" +
			message + "\r\n",
	)
	return smtp.SendMail(addr, auth, ch.SMTPFrom, ch.SMTPTo, msg)
}

func postJSON(url string, body []byte, headers map[string]string) error {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: DefaultTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return nil
}
