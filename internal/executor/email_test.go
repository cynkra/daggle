package executor

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildMIMEMessage_Plain(t *testing.T) {
	msg, err := buildMIMEMessage("alice@example.com", []string{"bob@example.com"}, nil, "Hello", "Hi there\n", nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s := string(msg)
	for _, want := range []string{
		"From: alice@example.com",
		"To: bob@example.com",
		"Subject:",
		"MIME-Version: 1.0",
		"multipart/mixed",
		"Content-Type: text/plain",
		"Hi there",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("expected %q in message, got:\n%s", want, s)
		}
	}
}

func TestBuildMIMEMessage_CC(t *testing.T) {
	msg, err := buildMIMEMessage("a@x", []string{"b@x"}, []string{"c@x", "d@x"}, "s", "body", nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(string(msg), "Cc: c@x, d@x") {
		t.Errorf("expected Cc header, got:\n%s", msg)
	}
}

func TestBuildMIMEMessage_Attachment(t *testing.T) {
	dir := t.TempDir()
	attachPath := filepath.Join(dir, "report.csv")
	payload := []byte("col1,col2\n1,2\n")
	if err := os.WriteFile(attachPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	msg, err := buildMIMEMessage("a@x", []string{"b@x"}, nil, "s", "body", []string{attachPath})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s := string(msg)

	if !strings.Contains(s, `filename="report.csv"`) {
		t.Errorf("expected filename header, got:\n%s", s)
	}
	if !strings.Contains(s, "Content-Transfer-Encoding: base64") {
		t.Errorf("expected base64 encoding header, got:\n%s", s)
	}

	// Verify the payload is present after base64 decoding the attachment part.
	// Locate the blank line after the filename header, then read until the
	// next MIME boundary.
	marker := `filename="report.csv"` + "\r\n\r\n"
	idx := strings.Index(s, marker)
	if idx < 0 {
		t.Fatalf("could not locate attachment payload in:\n%s", s)
	}
	rest := s[idx+len(marker):]
	end := strings.Index(rest, "\r\n--")
	if end < 0 {
		t.Fatalf("could not locate next boundary")
	}
	encoded := strings.ReplaceAll(rest[:end], "\r\n", "")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode: %v (encoded=%q)", err, encoded)
	}
	if string(decoded) != string(payload) {
		t.Errorf("decoded payload %q, want %q", decoded, payload)
	}
}

func TestWrapWriter_LineLength(t *testing.T) {
	// Drive enough bytes through the wrap writer to force at least two wraps,
	// then confirm no line in the produced output is longer than lineLen.
	var buf bytes.Buffer
	ww := &wrapWriter{w: &buf, lineLen: 10}
	data := []byte(strings.Repeat("a", 35))
	if _, err := ww.Write(data); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(buf.String(), "\r\n") {
		if len(line) > 10 {
			t.Errorf("line exceeds limit: %q", line)
		}
	}
}
