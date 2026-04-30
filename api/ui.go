package api

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cynkra/daggle/dag"
	"github.com/cynkra/daggle/state"
)

//go:embed ui/templates/*.html
var templateFS embed.FS

//go:embed ui/static/*
var staticFS embed.FS

var templates map[string]*template.Template

func init() {
	funcs := template.FuncMap{"timeAgo": timeAgo}
	pages := []string{"dag_list.html", "run_detail.html", "step_log.html"}
	templates = make(map[string]*template.Template, len(pages))
	for _, p := range pages {
		templates[p] = template.Must(
			template.New("").Funcs(funcs).ParseFS(templateFS, "ui/templates/layout.html", "ui/templates/"+p),
		)
	}
}

// registerUI adds UI routes to the server mux.
func (s *Server) registerUI() {
	// Static assets
	staticSub, _ := fs.Sub(staticFS, "ui/static")
	s.mux.Handle("GET /ui/static/", http.StripPrefix("/ui/static/", http.FileServer(http.FS(staticSub))))

	// Pages
	s.mux.HandleFunc("GET /ui/", s.uiDAGList)
	s.mux.HandleFunc("GET /ui/dags/{name}/runs/{run_id}", s.uiRunDetail)
	s.mux.HandleFunc("GET /ui/dags/{name}/runs/{run_id}/steps/{step_id}/log", s.uiStepLog)

	// Root redirect
	s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/", http.StatusFound)
	})
}

// --- View models ---

type dagListView struct {
	Title          string
	Nav            string
	RefreshSeconds int
	DAGs           []dagListItem
}

type dagListItem struct {
	Name       string
	Steps      int
	Project    string
	Schedule   string
	LastStatus string
	LastRun    string
	LastRunFmt string
	LastRunID  string
}

type runDetailView struct {
	Title          string
	Nav            string
	RefreshSeconds int
	Run            runViewModel
	Outputs        []OutputEntry
}

type runViewModel struct {
	RunID       string
	DAGName     string
	Status      string
	StartedFmt  string
	EndedFmt    string
	DurationFmt string
	RVersion    string
	Platform    string
	DAGHash     string
	Params      map[string]string
	Steps       []stepViewModel
}

type stepViewModel struct {
	StepID      string
	Status      string
	DurationFmt string
	Attempts    int
	Error       string
}

type stepLogView struct {
	Title          string
	Nav            string
	RefreshSeconds int
	DAGName        string
	RunID          string
	StepID         string
	Stdout         string
	Stderr         string
}

// --- Handlers ---

func (s *Server) uiDAGList(w http.ResponseWriter, _ *http.Request) {
	var dags []dagListItem

	for _, src := range s.sources() {
		entries, err := os.ReadDir(src.Dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
				continue
			}
			if name == "base.yaml" || name == "base.yml" {
				continue
			}

			path := filepath.Join(src.Dir, name)

			d, err := dag.ParseFileCached(path)
			if err != nil {
				continue
			}

			item := dagListItem{
				Name:    d.Name,
				Steps:   len(d.Steps),
				Project: src.Name,
			}
			if d.Trigger != nil {
				item.Schedule = d.Trigger.Schedule
			}

			if run, err := state.LatestRun(d.Name); err == nil && run != nil {
				item.LastStatus = state.RunStatus(run.Dir)
				item.LastRun = formatTime(run.StartTime)
				item.LastRunFmt = timeAgo(run.StartTime)
				item.LastRunID = run.ID
			}

			dags = append(dags, item)
		}
	}

	view := dagListView{
		Title:          "DAGs",
		Nav:            "dags",
		RefreshSeconds: 30,
		DAGs:           dags,
	}

	renderTemplate(w, "dag_list.html", view)
}

func (s *Server) uiRunDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	runID := r.PathValue("run_id")

	run, err := state.FindRun(name, runID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	detail := s.buildRunDetail(name, run)

	// Build step view models
	var steps []stepViewModel
	for _, st := range detail.Steps {
		steps = append(steps, stepViewModel{
			StepID:      st.StepID,
			Status:      st.Status,
			DurationFmt: formatDuration(st.DurationSeconds),
			Attempts:    st.Attempts,
			Error:       st.Error,
		})
	}

	// Parse outputs
	var outputs []OutputEntry
	events, _ := state.ReadEvents(run.Dir)
	stepIDs := make(map[string]bool)
	for _, e := range events {
		if e.StepID != "" {
			stepIDs[e.StepID] = true
		}
	}
	for stepID := range stepIDs {
		markers := ParseOutputMarkers(run.Dir, stepID)
		for k, v := range markers {
			outputs = append(outputs, OutputEntry{StepID: stepID, Key: k, Value: v})
		}
	}

	refresh := 0
	if detail.Status == "running" || detail.Status == "waiting" {
		refresh = 10
	}

	vm := runViewModel{
		RunID:       detail.RunID,
		DAGName:     detail.DAGName,
		Status:      detail.Status,
		StartedFmt:  formatTimePretty(detail.Started),
		EndedFmt:    formatTimePretty(detail.Ended),
		DurationFmt: formatDuration(detail.DurationSeconds),
		RVersion:    detail.RVersion,
		Platform:    detail.Platform,
		DAGHash:     detail.DAGHash,
		Params:      detail.Params,
		Steps:       steps,
	}

	view := runDetailView{
		Title:          name + " — " + detail.RunID,
		Nav:            "dags",
		RefreshSeconds: refresh,
		Run:            vm,
		Outputs:        outputs,
	}

	renderTemplate(w, "run_detail.html", view)
}

func (s *Server) uiStepLog(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	runID := r.PathValue("run_id")
	stepID := r.PathValue("step_id")

	if !isValidStepID(stepID) {
		http.Error(w, "invalid step_id", http.StatusBadRequest)
		return
	}

	run, err := state.FindRun(name, runID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	stdout, _ := os.ReadFile(filepath.Join(run.Dir, stepID+".stdout.log"))
	stderr, _ := os.ReadFile(filepath.Join(run.Dir, stepID+".stderr.log"))

	refresh := 0
	status := state.RunStatus(run.Dir)
	if status == "running" || status == "waiting" {
		refresh = 5
	}

	view := stepLogView{
		Title:          stepID + " — log",
		Nav:            "dags",
		RefreshSeconds: refresh,
		DAGName:        name,
		RunID:          run.ID,
		StepID:         stepID,
		Stdout:         string(stdout),
		Stderr:         string(stderr),
	}

	renderTemplate(w, "step_log.html", view)
}

// --- Helpers ---

func renderTemplate(w http.ResponseWriter, name string, data interface{}) {
	t, ok := templates[name]
	if !ok {
		http.Error(w, "unknown template: "+name, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

func formatDuration(seconds float64) string {
	if seconds <= 0 {
		return "—"
	}
	d := time.Duration(seconds * float64(time.Second))
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	m := int(d.Minutes())
	s := int(math.Mod(d.Seconds(), 60))
	return fmt.Sprintf("%dm %ds", m, s)
}

func formatTimePretty(rfc string) string {
	if rfc == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, rfc)
	if err != nil {
		return rfc
	}
	return t.Format("2006-01-02 15:04:05")
}

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}
