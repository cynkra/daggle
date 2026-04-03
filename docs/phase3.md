# Phase 3 — Triggers & New Step Types

Phase 3 adds a unified trigger system with six trigger types, four new R step types, cancellation support, and system diagnostics — making daggle a fully automated workflow daemon.

## What was built

### Unified trigger system

All triggers live under a `trigger:` block in DAG YAML. Triggers are additive — any matching trigger starts a run. Manual execution via `daggle run` is always available regardless of trigger configuration.

### Trigger types

| Trigger | YAML field | Mechanism |
|---------|-----------|-----------|
| Cron schedule | `trigger.schedule` | robfig/cron expressions (migrated from top-level `schedule:`) |
| File watcher | `trigger.watch` | fsnotify file events with glob pattern matching and configurable debounce |
| Webhook | `trigger.webhook` | HTTP POST to `/webhook/{dag}` with optional HMAC-SHA256 validation |
| DAG completion | `trigger.on_dag` | Event listener, triggers on upstream DAG completion or failure |
| Condition polling | `trigger.condition` | R expression or shell command evaluated on interval (default 5m) |
| Git changes | `trigger.git` | Polls for new commits by branch (default 30s) |

### Overlap policies

DAG-level `trigger.overlap` field controls what happens when a trigger fires while the DAG is already running:

- `skip` (default) — ignore the trigger
- `cancel` — kill the old run and start fresh

Max 4 concurrent DAG runs enforced by the scheduler.

### New step types

| Type | YAML field | What it does |
|------|-----------|----------|
| R Markdown | `rmd:` | Renders via `rmarkdown::render()` |
| Restore renv | `renv_restore:` | Runs `renv::restore(prompt = FALSE)` to install packages |
| Coverage | `coverage:` | Runs `covr::package_coverage()` with percentage output |
| Validate | `validate:` | Runs a data validation R script via Rscript |

All new R step types check for required packages and fail with a clear install instruction if missing.

### Cancellation & process management

- `daggle stop` sends SIGTERM to the scheduler daemon via PID file
- Graceful shutdown: stops scheduling new runs, waits up to 5 minutes for in-flight runs to complete
- Force cancellation fallback after grace period

### Graceful daemon lifecycle

- SIGINT/SIGTERM: stop scheduling, wait for in-flight runs, clean shutdown
- SIGHUP: hot-reload DAG files and configuration without restart
- Scheduler manages all trigger goroutines and webhook server lifecycle

### daggle doctor

Diagnostics command checking system health:

- R version and platform detection
- renv detection and library status
- Scheduler running status with PID
- DAG directory and data directory paths
- DAG file validation (parse errors)
- Output with OK/WARN/FAIL/INFO status levels

## New files

```
executor/rmd.go          — R Markdown rendering via rmarkdown::render()
executor/validate.go     — Data validation script runner
scheduler/webhook.go     — Webhook HTTP server and HMAC-SHA256 validation
cli/stop.go              — daggle stop command (SIGTERM via PID file)
cli/doctor.go            — daggle doctor system diagnostics
```

## Modified files

```
dag/dag.go               — Trigger struct with all trigger type fields, overlap policy
dag/validate.go          — Validation for trigger fields and new step types
executor/executor.go     — Factory routing for rmd, renv_restore, coverage, validate
executor/rpkg.go         — Added renv_restore and coverage actions
scheduler/scheduler.go   — Full trigger system, overlap policies, graceful shutdown
cli/serve.go             — Webhook port configuration
```

## Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/fsnotify/fsnotify` | File system event watching for trigger.watch |
