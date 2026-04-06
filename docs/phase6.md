# Phase 6 — Minimal Status UI

Phase 6 adds a read-only status dashboard embedded in the Go binary via `go:embed`, served alongside the REST API when `daggle serve --port` is used.

## What was built

### Embedded web UI

- **Technology:** Go HTML templates + CSS, no JS framework, no build step
- **Embedding:** `go:embed` directive includes templates and static assets in the binary
- **Integration:** Registered as routes on the existing API server mux

### Pages

| Page | Route | Description |
|------|-------|-------------|
| DAG list | `GET /ui/` | Table with name, step count, schedule, last status, last run time |
| Run detail | `GET /ui/dags/{name}/runs/{run_id}` | Run metadata, step status table, outputs |
| Step log | `GET /ui/dags/{name}/runs/{run_id}/steps/{step_id}/log` | stdout/stderr in scrollable blocks |

Root `/` redirects to `/ui/`.

### Auto-refresh

Pages auto-refresh via `<meta http-equiv="refresh">` when a DAG is running:

- DAG list: every 30 seconds (always)
- Run detail: every 10 seconds (when status is running/waiting)
- Step log: every 5 seconds (when parent run is running/waiting)

### Design

- Reuses the daggle docs site palette (Canopy Green, Deep Forest, Sap Gold, Aurora Teal)
- Status badges with color coding: completed (green), running (teal), failed (red), waiting (gold), idle (gray)
- Cinzel serif headings (matching docs site Norse/Ratatoskr theme)
- Inter body font, system monospace for logs

### Source files

| File | Purpose |
|------|---------|
| `api/ui.go` | Embed directive, template parsing, view models, page handlers, helpers |
| `api/ui/templates/layout.html` | Base HTML template (head, header, footer) |
| `api/ui/templates/dag_list.html` | DAG list table |
| `api/ui/templates/run_detail.html` | Run detail with steps and outputs |
| `api/ui/templates/step_log.html` | stdout/stderr log viewer |
| `api/ui/static/style.css` | All styles (~160 lines) |

### Design decisions

- **Read-only** — no trigger, approve, or reject buttons. Use CLI, API, or daggleR for mutations.
- **No JS framework** — server-rendered HTML with meta-refresh. Keeps the binary small and eliminates build tooling.
- **API is the product** — the UI is a convenience viewer. Custom dashboards should wrap the REST API with daggleR + Shiny or any HTTP client.
- **Same package** — UI handlers live in the `api` package (not a sub-package) to reuse internal methods like `findRun`, `buildRunDetail`, `buildStepSummaries`.
