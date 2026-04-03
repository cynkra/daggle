# Architecture

daggle is a single Go binary that orchestrates R workflows defined in YAML. It spawns `Rscript` (or `quarto`, `sh`) subprocesses, captures their output, and manages lifecycle events — all without embedding R or requiring a database.

## System overview

```
                          daggle (single binary)
┌───────────────────────────────────────────────────────────┐
│                                                           │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐ │
│  │   CLI    │  │Scheduler │  │ Web UI   │  │ REST API │ │
│  │  (cobra) │  │ (triggers│  │(planned) │  │(planned) │ │
│  └────┬─────┘  └────┬─────┘  └──────────┘  └──────────┘ │
│       │              │                                    │
│       ▼              ▼                                    │
│  ┌────────────────────────────────────────────┐          │
│  │              Core Engine                    │          │
│  │  topo sort → tier walking → step execution  │          │
│  │  retries, hooks, output passing, metadata   │          │
│  └──────────────────┬─────────────────────────┘          │
│                     │                                     │
│  ┌──────────────────┼──────────────────────┐             │
│  │          Executor Layer                  │             │
│  │                                          │             │
│  │  ScriptExecutor   (Rscript <file>)       │             │
│  │  InlineRExecutor  (Rscript -e <expr>)    │             │
│  │  ShellExecutor    (sh -c <cmd>)          │             │
│  │  QuartoExecutor   (quarto render <path>) │             │
│  │  RPkgExecutor     (devtools/rcmdcheck/..│             │
│  │  ConnectExecutor  (rsconnect deploy)     │             │
│  │  RmdExecutor      (rmarkdown::render()) │             │
│  │  ValidateExecutor (validation scripts)  │             │
│  │  ApproveExecutor  (approval gate)       │             │
│  │  CallExecutor     (sub-DAG)             │             │
│  │  PinExecutor      (pins publish)        │             │
│  │  VetiverExecutor  (MLOps deploy)        │             │
│  └──────────────────┬──────────────────────┘             │
│                     │                                     │
│  ┌──────────────────┼──────────────────────┐             │
│  │           State Layer                    │             │
│  │  JSONL events · meta.json · log files    │             │
│  │  XDG paths · run directories             │             │
│  └──────────────────────────────────────────┘             │
└───────────────────────────────────────────────────────────┘
         │
         ▼
   ┌───────────┐
   │  Rscript   │  ← only external dependency
   │  quarto    │  ← optional (for quarto: steps)
   └───────────┘
```

## Package layout

```
cmd/daggle/       Entry point (main.go)
cli/              Cobra commands: run, validate, status, list, serve
dag/              YAML parsing, validation, topo sort, template expansion
engine/           DAG orchestration: tier walking, retries, hooks, output passing
executor/         Process supervision: one type per step kind
renv/             renv.lock detection and R_LIBS_USER resolution
scheduler/        Cron scheduling, PID file management
state/            XDG paths, JSONL events, run directories, metadata
```

## Data flow

```
YAML file
  │
  ▼
dag.ParseFile()        Parse YAML, validate, set SourceDir
  │
  ▼
dag.ExpandDAG()        Expand {{ .Today }}, {{ .Params.x }}, etc.
  │
  ▼
renv.Detect()          Auto-detect renv.lock, resolve R_LIBS_USER
  │
  ▼
dag.TopoSort()         Group steps into parallel tiers via Kahn's algorithm
  │
  ▼
engine.Run()           Walk tiers sequentially
  │
  ├── For each tier: run steps concurrently (goroutines + WaitGroup)
  │     │
  │     ├── executor.New(step)     Select executor by step type
  │     ├── executor.Run()         Spawn subprocess, capture I/O
  │     │     │
  │     │     ├── stdout → log file + terminal (markers stripped)
  │     │     ├── stderr → log file + terminal
  │     │     └── ::daggle-output:: markers → Result.Outputs
  │     │
  │     ├── On success: collect outputs, run on_success hook
  │     ├── On failure: retry (with backoff), then run on_failure hook
  │     └── Write JSONL events for each state transition
  │
  ├── After all tiers: run DAG-level hooks (on_exit, on_success/on_failure)
  └── Write final meta.json (status, end time)
```

## Process model

daggle does not embed R. Every R step spawns a fresh `Rscript` process:

- **Process groups** (`Setpgid: true`) — ensures child processes (e.g. R spawning further processes) are killed on timeout
- **Timeout enforcement** — context deadline triggers SIGTERM to the process group, 5s grace period, then SIGKILL
- **Environment isolation** — each step gets: parent env + DAG env + renv `R_LIBS_USER` (if detected) + step env + accumulated `DAGGLE_OUTPUT_*` vars + metadata (`DAGGLE_RUN_ID`, `DAGGLE_DAG_NAME`, `DAGGLE_RUN_DIR`)
- **Log capture** — stdout and stderr written to per-step log files; stdout also parsed line-by-line for `::daggle-output::` markers

## State and storage

All state is file-based, following XDG conventions:

```
~/.config/daggle/              Config (planned)
  dags/                        Global DAG definitions

~/.local/share/daggle/         Data
  runs/
    <dag-name>/
      <YYYY-MM-DD>/
        run_<xid>/
          events.jsonl         Lifecycle events (step started/completed/failed/retried)
          meta.json            Reproducibility metadata (R version, DAG hash, platform)
          <step>.stdout.log    Captured stdout
          <step>.stderr.log    Captured stderr
          <step>.inline.R      Generated R code (for r_expr, rpkg, connect steps)
  proc/
    scheduler.pid              Scheduler process ID file
```

- **events.jsonl** — append-only, one JSON object per line, thread-safe via mutex
- **meta.json** — written at run start (DAG hash, R version, renv detection), updated at run end (status, end time)
- **Run IDs** — xid-based, globally unique, time-sortable

## Executor architecture

All executors implement a single interface:

```go
type Executor interface {
    Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result
}
```

Every executor delegates to `runProcess()` which handles process groups, log capture, output marker parsing, and timeout enforcement. Adding a new step type requires:

1. Add field to `Step` struct in `dag/dag.go`
2. Update `StepType()` and validation
3. Create executor implementing the interface
4. Add case to `executor.New()` factory

## Scheduler & Trigger System

The scheduler (`daggle serve`) runs as a long-lived daemon managing all trigger types:

- Uses `robfig/cron/v3` for cron expression parsing
- **Hot reload** — re-scans DAG directory every 30s, detects changes via SHA-256 file hashing
- **Overlap protection** — skips a scheduled run if the same DAG is still executing
- **Concurrency limit** — max 4 simultaneous DAG runs (configurable)
- **Graceful shutdown** — on SIGINT/SIGTERM, stops scheduling and waits up to 5 minutes for in-flight runs

### Trigger types

All triggers live under a unified `trigger:` block in DAG YAML. Triggers are additive — any matching trigger starts a run.

| Trigger | Mechanism | Use case |
|---------|-----------|----------|
| `schedule` | Cron expressions via robfig/cron | Recurring pipelines |
| `watch` | fsnotify file events with debounce | Data drops, config changes |
| `webhook` | HTTP POST with HMAC-SHA256 validation | GitHub hooks, external systems |
| `on_dag` | Event listener on DAG completion | Multi-DAG chaining |
| `condition` | R expr / shell command polling | DB changes, API readiness |
| `git` | Poll for new commits by branch | Local CI |

## Dependencies

| Package | Purpose |
|---------|---------|
| `gopkg.in/yaml.v3` | YAML parsing with strict field validation |
| `github.com/spf13/cobra` | CLI framework |
| `github.com/rs/xid` | Time-sortable unique run IDs |
| `github.com/robfig/cron/v3` | Cron expression parsing |
| `github.com/fsnotify/fsnotify` | File system event watching for trigger.watch |

Total: 5 external Go dependencies. R and quarto are runtime dependencies only.

## Design principles

1. **R is the default** — every step is assumed to be R unless stated otherwise; shell is the escape hatch
2. **No database** — files are the database; JSONL events, flat logs, XDG paths
3. **No embedded R** — daggle supervises `Rscript` processes, never links against R
4. **Single binary** — `go build` produces one static binary; R is the only prerequisite
5. **Minimal dependencies** — 5 Go packages; no runtime services (no Redis, no Postgres)
6. **Fail loud** — strict YAML parsing (`KnownFields`), R errors surfaced in status, clear messages for missing packages
