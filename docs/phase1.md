# Phase 1 — Complete

Phase 1 delivers a fully functional DAG scheduler for R: define workflows in YAML, execute them with dependency resolution, and monitor results — all from a single Go binary.

## What was built

### Core engine

- **YAML DAG parser** — Parses DAG definitions into Go structs using `gopkg.in/yaml.v3` with strict field validation
- **Topological sort** — Kahn's algorithm groups steps into tiers; steps within a tier run in parallel via goroutines
- **Template expansion** — Go `text/template` support in YAML fields: `{{ .Today }}`, `{{ .Now }}`, `{{ .Params.x }}`, `{{ .Env.KEY }}`

### Step types

| Type | YAML field | Behavior |
|------|-----------|----------|
| R script | `script:` | Runs `Rscript --no-save --no-restore <path> [args]` |
| Inline R | `r_expr:` | Writes expression to temp `.R` file in run directory, runs via Rscript |
| Shell | `command:` | Runs via `sh -c` |

### Execution

- **Retries** — Configurable per step via `retry: { limit: N }` with linear backoff between attempts
- **Timeouts** — Per-step timeout enforcement via `timeout:` field. On expiry: SIGTERM to entire process group, 5s grace period, then SIGKILL. No orphaned R processes.
- **Working directory** — Steps execute in the DAG file's directory by default. Overridable at DAG level (`workdir:`) and step level. Precedence: step > DAG > DAG file directory.
- **Parallel execution** — Independent steps (same tier in the topological sort) run concurrently

### Inter-step communication

- **Output markers** — Steps emit `::rdag-output name=key::value` on stdout. rdag parses these, strips them from terminal output (keeps in log files), and passes to downstream steps as `RDAG_OUTPUT_<STEP_ID>_<KEY>` environment variables
- **Environment propagation** — DAG-level env, step-level env, and rdag metadata (`RDAG_RUN_ID`, `RDAG_DAG_NAME`, `RDAG_RUN_DIR`) are available to all steps

### Lifecycle hooks

- **DAG-level:** `on_success`, `on_failure`, `on_exit` — run after DAG completes
- **Step-level:** `on_success`, `on_failure` — run after individual step completes
- Hooks can be R expressions (`r_expr:`) or shell commands (`command:`)
- Hooks receive all run metadata and accumulated outputs as environment variables

### Cron scheduler

- **`rdag serve`** — Daemon that monitors DAG files for `schedule:` fields and triggers runs on cron expressions
- Uses `robfig/cron/v3` for cron parsing (standard 5-field expressions plus `@every`, `@hourly`, etc.)
- **Skip-on-overlap** — If a DAG is still running when its next tick fires, the run is skipped
- **Max concurrent** — Limits to 4 simultaneous DAG runs
- **Hot reload** — Re-scans DAG directory every 30s, detects new/changed/removed DAGs via SHA-256 content hashing
- **PID file** — Written to `~/.local/share/rdag/proc/scheduler.pid` for process management
- **Graceful shutdown** — On SIGTERM/SIGINT: stops scheduling new runs, waits up to 5 minutes for in-flight runs to complete

### State & persistence

- **JSONL events** — Each run records lifecycle events (run_started, step_started, step_completed, step_failed, step_retrying, run_completed, run_failed) to `events.jsonl`
- **Log capture** — stdout and stderr captured to per-step log files
- **XDG-compliant** — Config in `~/.config/rdag/`, data in `~/.local/share/rdag/`
- **Run directories** — `~/.local/share/rdag/runs/<dag>/<date>/run_<xid>/` with xid-based sortable IDs

### DAG discovery

- **Project-local** — `.rdag/` directory in current working directory is auto-discovered
- **Global** — `~/.config/rdag/dags/` as fallback
- **Explicit** — `--dags-dir` flag overrides all

### CLI

| Command | Description |
|---------|-------------|
| `rdag run <dag> [-p key=value]` | Run a DAG immediately with optional parameter overrides |
| `rdag validate <dag\|path>` | Parse, validate, and show execution plan (tiers) |
| `rdag status <dag> [--run-id]` | Show step-by-step results of latest or specific run |
| `rdag list` | List all DAGs with step count, last run time, and status |
| `rdag serve` | Start the cron scheduler daemon |

## Architecture

```
cmd/rdag/main.go     — entry point
cli/                 — cobra commands (run, validate, status, list, serve)
dag/                 — YAML parsing, validation, topo sort, template expansion
executor/            — process supervision (Rscript, inline R, shell)
engine/              — DAG orchestration (tier walking, retries, hooks, output passing)
state/               — XDG paths, JSONL events, run directory management
scheduler/           — cron scheduler, PID file management
```

## Dependencies

| Package | Purpose |
|---------|---------|
| `gopkg.in/yaml.v3` | YAML parsing |
| `github.com/spf13/cobra` | CLI framework |
| `github.com/rs/xid` | Sortable unique run IDs |
| `github.com/robfig/cron/v3` | Cron expression parsing and scheduling |

## Tests

28 tests across 5 packages covering: YAML parsing, validation edge cases, cycle detection, topo sort tiers, template expansion, shell execution, timeout/signal handling, working directory, output marker parsing, engine orchestration (linear, parallel, failure propagation, retries, output passing, workdir resolution), JSONL event round-trips, XDG paths, PID file management, DAG scanning, hot reload, overlap skipping, max concurrency, and scheduler start/stop.
