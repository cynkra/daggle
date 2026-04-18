// Package tui provides a bubbletea-based terminal dashboard for live DAG runs.
package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/cynkra/daggle/state"
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	headerStyle = lipgloss.NewStyle().Bold(true).Underline(true)
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	failStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

type eventMsg state.Event
type errMsg struct{ err error }
type endMsg struct{}
type tickMsg time.Time

// Config controls how the TUI connects to the SSE stream.
type Config struct {
	BaseURL string // e.g. http://localhost:8080
	DAG     string
	RunID   string // "latest" resolves to the most recent run
}

// Run starts the bubbletea program that streams live events.
func Run(ctx context.Context, cfg Config) error {
	events := make(chan tea.Msg, 64)
	go streamFromHTTP(ctx, cfg, events)

	m := newModel(cfg, events)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

type model struct {
	cfg      Config
	events   <-chan tea.Msg
	steps    []state.StepState
	stepIdx  map[string]int
	status   string
	started  time.Time
	err      error
	done     bool
	width    int
	height   int
	annCount int
}

func newModel(cfg Config, events <-chan tea.Msg) model {
	return model{
		cfg:     cfg,
		events:  events,
		stepIdx: map[string]int{},
		status:  "connecting",
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(waitForEvent(m.events), tickCmd())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case eventMsg:
		m.applyEvent(state.Event(msg))
		return m, waitForEvent(m.events)
	case endMsg:
		m.done = true
		return m, nil
	case errMsg:
		m.err = msg.err
		return m, nil
	case tickMsg:
		return m, tickCmd()
	}
	return m, nil
}

func (m *model) applyEvent(e state.Event) {
	if m.started.IsZero() && !e.Timestamp.IsZero() {
		m.started = e.Timestamp
	}
	switch e.Type {
	case state.EventRunStarted:
		m.status = "running"
	case state.EventRunCompleted:
		m.status = "completed"
	case state.EventRunFailed:
		m.status = "failed"
	case state.EventRunAnnotated:
		m.annCount++
	}
	if e.StepID == "" {
		return
	}
	idx, ok := m.stepIdx[e.StepID]
	if !ok {
		m.steps = append(m.steps, state.StepState{StepID: e.StepID})
		idx = len(m.steps) - 1
		m.stepIdx[e.StepID] = idx
	}
	ss := &m.steps[idx]
	switch e.Type {
	case state.EventStepStarted:
		ss.Status = "running"
		ss.Attempts = e.Attempt
	case state.EventStepCompleted:
		ss.Status = "completed"
		if d, err := time.ParseDuration(e.Duration); err == nil {
			ss.Duration = d
		}
		ss.PeakRSSKB = e.PeakRSSKB
	case state.EventStepFailed:
		ss.Status = "failed"
		ss.Error = e.Error
		if d, err := time.ParseDuration(e.Duration); err == nil {
			ss.Duration = d
		}
	case state.EventStepRetrying:
		ss.Status = "retrying"
	case state.EventStepCached:
		ss.Status = "cached"
		ss.Cached = true
	case state.EventStepWaitApproval:
		ss.Status = "waiting"
	case state.EventStepApproved:
		ss.Status = "approved"
	case state.EventStepRejected:
		ss.Status = "rejected"
	}
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("daggle monitor — %s", m.cfg.DAG)))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("run: %s   status: %s", m.cfg.RunID, statusSymbol(m.status))))
	b.WriteString("\n\n")
	b.WriteString(headerStyle.Render(fmt.Sprintf("%-24s %-14s %-10s %s", "STEP", "STATUS", "DURATION", "PEAK RSS")))
	b.WriteString("\n")
	for _, s := range m.steps {
		dur := "-"
		if s.Duration > 0 {
			dur = s.Duration.String()
		}
		rss := "-"
		if s.PeakRSSKB > 0 {
			rss = fmt.Sprintf("%dKB", s.PeakRSSKB)
		}
		fmt.Fprintf(&b, "%-24s %-14s %-10s %s\n", truncate(s.StepID, 24), statusSymbol(s.Status), dur, rss)
	}
	if m.annCount > 0 {
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("annotations: %d", m.annCount)))
		b.WriteString("\n")
	}
	if m.err != nil {
		b.WriteString("\n")
		b.WriteString(failStyle.Render("error: " + m.err.Error()))
		b.WriteString("\n")
	}
	if m.done {
		b.WriteString("\n")
		b.WriteString(okStyle.Render("stream ended — press q to exit"))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("q / esc to quit"))
	return b.String()
}

func statusSymbol(s string) string {
	switch s {
	case "completed", "approved":
		return okStyle.Render("✓ " + s)
	case "failed", "rejected":
		return failStyle.Render("✗ " + s)
	case "waiting", "retrying":
		return warnStyle.Render("⏳ " + s)
	case "running":
		return "• " + s
	case "":
		return dimStyle.Render("-")
	default:
		return s
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// waitForEvent blocks until the next message arrives on ch, then returns it.
// When ch is closed, it returns endMsg{} so the UI can flip to "ended".
func waitForEvent(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return endMsg{}
		}
		return msg
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// streamFromHTTP opens the SSE endpoint and forwards each event to ch.
// Closes ch when the stream ends (or errors).
func streamFromHTTP(ctx context.Context, cfg Config, ch chan<- tea.Msg) {
	defer close(ch)

	url := fmt.Sprintf("%s/api/v1/dags/%s/runs/%s/stream",
		strings.TrimRight(cfg.BaseURL, "/"), cfg.DAG, cfg.RunID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		ch <- errMsg{err}
		return
	}
	req.Header.Set("Accept", "text/event-stream")

	// SSE is long-lived, so the overall Timeout must be 0 — but we still
	// need guards against a hung upstream that never writes response
	// headers, or a TCP dial that never completes.
	client := &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		ch <- errMsg{err}
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		ch <- errMsg{fmt.Errorf("stream HTTP %d", resp.StatusCode)}
		return
	}

	parseSSE(resp.Body, ch)
}

// parseSSE reads SSE frames from r and forwards each payload to ch.
// On `event: end` or `event: error`, sends the appropriate final msg and returns.
func parseSSE(r io.Reader, ch chan<- tea.Msg) {
	scanner := bufio.NewScanner(r)
	// Event lines are JSON objects — typical size is a few KB. 256 KB is
	// plenty of headroom; oversize lines trip bufio.ErrTooLong, which
	// bubbles up via scanner.Err() as an errMsg so the user sees a real
	// error instead of the stream silently ending.
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	var eventName string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			eventName = ""
		case strings.HasPrefix(line, "event: "):
			eventName = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data := strings.TrimPrefix(line, "data: ")
			switch eventName {
			case "end":
				ch <- endMsg{}
				return
			case "error":
				ch <- errMsg{fmt.Errorf("%s", data)}
				return
			case "timeout":
				ch <- endMsg{}
				return
			default:
				var e state.Event
				if err := json.Unmarshal([]byte(data), &e); err == nil {
					ch <- eventMsg(e)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		ch <- errMsg{err}
	}
}
