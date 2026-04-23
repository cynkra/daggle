package executor

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
)

// EmailExecutor sends an email via a named SMTP notification channel.
// Runs entirely in-process via net/smtp — no R, no subprocess.
type EmailExecutor struct{}

// Run builds a multipart MIME message and sends it via the configured SMTP
// channel. Attachment paths are resolved relative to the step's workdir.
func (e *EmailExecutor) Run(ctx context.Context, step dag.Step, logDir, workdir string, _ []string) Result {
	start := time.Now()

	email := step.Email
	cfg, err := state.LoadConfig()
	if err != nil {
		return emailErr(start, fmt.Errorf("load config: %w", err))
	}
	channel, ok := cfg.Notifications[email.Channel]
	if !ok {
		return emailErr(start, fmt.Errorf("email.channel %q is not defined in config.yaml notifications", email.Channel))
	}
	if channel.Type != "smtp" {
		return emailErr(start, fmt.Errorf("email.channel %q has type %q; expected \"smtp\"", email.Channel, channel.Type))
	}

	from := email.From
	if from == "" {
		from = channel.SMTPFrom
	}
	to := email.To
	if len(to) == 0 {
		to = channel.SMTPTo
	}
	if from == "" {
		return emailErr(start, fmt.Errorf("no sender: set email.from or smtp_from on channel %q", email.Channel))
	}
	if len(to) == 0 {
		return emailErr(start, fmt.Errorf("no recipients: set email.to or smtp_to on channel %q", email.Channel))
	}

	body, err := resolveEmailBody(email, workdir)
	if err != nil {
		return emailErr(start, err)
	}

	msg, err := buildMIMEMessage(from, to, email.Cc, email.Subject, body, resolveAttachPaths(email.Attach, workdir))
	if err != nil {
		return emailErr(start, err)
	}

	// Assemble the full recipient list for the SMTP envelope (includes BCC).
	recipients := append([]string{}, to...)
	recipients = append(recipients, email.Cc...)
	recipients = append(recipients, email.Bcc...)

	addr := fmt.Sprintf("%s:%d", channel.SMTPHost, channel.SMTPPort)
	var auth smtp.Auth
	if channel.SMTPUser != "" {
		auth = smtp.PlainAuth("", channel.SMTPUser, channel.SMTPPassword, channel.SMTPHost)
	}
	if err := smtp.SendMail(addr, auth, from, recipients, msg); err != nil {
		return emailErr(start, fmt.Errorf("smtp send: %w", err))
	}

	// Write a small log marker so `daggle logs` shows what happened.
	_ = appendStepLog(logDir, step.ID, fmt.Sprintf(
		"sent email via channel %q\n  from: %s\n  to: %s\n  subject: %s\n  attachments: %d\n",
		email.Channel, from, strings.Join(to, ", "), email.Subject, len(email.Attach)))

	return Result{
		ExitCode: 0,
		Duration: time.Since(start),
		Outputs: map[string]string{
			"recipients": fmt.Sprintf("%d", len(recipients)),
		},
	}
}

func emailErr(start time.Time, err error) Result {
	return Result{ExitCode: 1, Err: err, Duration: time.Since(start)}
}

func resolveEmailBody(e *dag.EmailStep, workdir string) (string, error) {
	if e.Body != "" {
		return e.Body, nil
	}
	p := e.BodyFile
	if !filepath.IsAbs(p) && workdir != "" {
		p = filepath.Join(workdir, p)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("read body_file %s: %w", p, err)
	}
	return string(data), nil
}

func resolveAttachPaths(attachments []string, workdir string) []string {
	out := make([]string, len(attachments))
	for i, a := range attachments {
		if filepath.IsAbs(a) || workdir == "" {
			out[i] = a
		} else {
			out[i] = filepath.Join(workdir, a)
		}
	}
	return out
}

// buildMIMEMessage returns a multipart/mixed RFC 2822 message with the body
// as a text/plain part and each attachment base64-encoded.
func buildMIMEMessage(from string, to, cc []string, subject, body string, attachments []string) ([]byte, error) {
	var buf bytes.Buffer
	boundary := fmt.Sprintf("daggle-%d", time.Now().UnixNano())

	hdr := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, v) }
	hdr("From", from)
	hdr("To", strings.Join(to, ", "))
	if len(cc) > 0 {
		hdr("Cc", strings.Join(cc, ", "))
	}
	hdr("Subject", mime.QEncoding.Encode("utf-8", subject))
	hdr("MIME-Version", "1.0")
	hdr("Content-Type", fmt.Sprintf(`multipart/mixed; boundary=%q`, boundary))
	buf.WriteString("\r\n")

	// Body part
	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	buf.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	buf.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		buf.WriteString("\r\n")
	}

	// Attachments
	for _, path := range attachments {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read attachment %s: %w", path, err)
		}
		ctype := mime.TypeByExtension(filepath.Ext(path))
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		fmt.Fprintf(&buf, "--%s\r\n", boundary)
		fmt.Fprintf(&buf, "Content-Type: %s\r\n", ctype)
		buf.WriteString("Content-Transfer-Encoding: base64\r\n")
		fmt.Fprintf(&buf, "Content-Disposition: attachment; filename=%q\r\n", filepath.Base(path))
		buf.WriteString("\r\n")
		enc := base64.NewEncoder(base64.StdEncoding, &wrapWriter{w: &buf, lineLen: 76})
		if _, err := enc.Write(data); err != nil {
			return nil, err
		}
		_ = enc.Close()
		buf.WriteString("\r\n")
	}

	fmt.Fprintf(&buf, "--%s--\r\n", boundary)
	return buf.Bytes(), nil
}

// wrapWriter wraps writes so that no line exceeds lineLen characters. Used to
// keep base64 attachment chunks within SMTP's practical line-length limits.
type wrapWriter struct {
	w       *bytes.Buffer
	lineLen int
	col     int
}

func (w *wrapWriter) Write(p []byte) (int, error) {
	written := 0
	for _, b := range p {
		if w.col >= w.lineLen {
			w.w.WriteString("\r\n")
			w.col = 0
		}
		w.w.WriteByte(b)
		w.col++
		written++
	}
	return written, nil
}

// appendStepLog appends a line to the step's stdout log file.
func appendStepLog(logDir, stepID, line string) error {
	p := filepath.Join(logDir, stepID+".stdout.log")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.WriteString(line)
	return err
}
