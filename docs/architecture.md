# Architecture

daggle is a single Go binary that orchestrates R workflows defined in YAML. It spawns `Rscript` (or `quarto`, `sh`) subprocesses, captures their output, and manages lifecycle events — all without embedding R or requiring a database.

## System overview

```
                          daggle (single binary)
┌───────────────────────────────────────────────────────────┐
│                                                           │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐ │
│  │   CLI    │  │Scheduler │  │ Web UI   │  │ REST API │ │
│  │  (cobra) │  │ (triggers│  │(go:embed)│  │ (api/)   │ │
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
│  │          Executor Layer (15 impls)       │             │
│  │                                          │             │
│  │  ScriptExecutor   (Rscript <file>)       │             │
│  │  InlineRExecutor  (Rscript -e <expr>)    │             │
│  │  ShellExecutor    (sh -c <cmd>)          │             │
│  │  QuartoExecutor   (quarto render <path>) │             │
│  │  RPkgExecutor     (test/check/document/ │             │
│  │                    lint/style/coverage/  │             │
│  │                    renv_restore/install/ │             │
│  │                    pkgdown/shinytest/    │             │
│  │                    targets/benchmark/    │             │
│  │                    revdepcheck)          │             │
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
   ┌───────────┐         ┌──────────────────────────────┐
   │  Rscript   │  ← only │  daggleR (companion R pkg)   │
   │  quarto    │  ext.   │  cynkra/daggleR               │
   └───────────┘  dep.    │                              │
                          │  In-step helpers:            │
                          │    daggle_output(), ...      │
                          │    (env vars + stdout)       │
                          │                              │
                          │  API wrappers:               │
                          │    daggle_list_dags(), ...   │
                          │    (httr2 → REST API)        │
                          └──────────────────────────────┘
```

## Package layout

```
cmd/daggle/       Entry point (main.go)
api/              REST API server (21 endpoints): handlers, response types, embedded UI
cache/            Step-level caching: key computation, on-disk cache store
cli/              Cobra commands (20 subcommands): run, plan, diff, logs, status, serve, etc.
dag/              YAML parsing, validation, topo sort, template expansion, matrix, secrets
engine/           DAG orchestration: tier walking, retries, hooks, caching, artifacts, freshness
executor/         Process supervision: 15 executor implementations for 24 step types
examples/         Example DAG projects
renv/             renv.lock detection and R_LIBS_USER resolution
scheduler/        Cron scheduling, PID file management, webhook server, deadline alerting
state/            XDG paths, JSONL events, run directories, metadata, config, cleanup
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
  │     ├── Check when condition → skip if false
  │     ├── Check preconditions → fail if not met
  │     ├── Check freshness → fail/warn if sources stale
  │     ├── Check cache → skip if hit (replay outputs)
  │     │
  │     ├── executor.New(step)     Select executor by step type
  │     ├── executor.Run()         Spawn subprocess, capture I/O
  │     │     │
  │     │     ├── stdout → log file + terminal (markers stripped)
  │     │     ├── stderr → log file + terminal
  │     │     └── ::daggle-output/summary/meta/validation:: → Result
  │     │
  │     ├── On success: collect outputs, verify artifacts, write
  │     │   summary/meta/validation files, save cache, run hook
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
- **Log capture** — stdout and stderr written to per-step log files; stdout parsed line-by-line for four marker protocols: `::daggle-output::` (inter-step data), `::daggle-summary::` (rich summaries), `::daggle-meta::` (typed metadata), `::daggle-validation::` (validation results). See [protocols.md](protocols.md)

## State and storage

All state is file-based, following XDG conventions:

```
~/.config/daggle/              Config
  config.yaml                  Global config (cleanup settings)
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
          <step>.summary.md   Step summary (if ::daggle-summary:: emitted)
          <step>.meta.json    Typed metadata (if ::daggle-meta:: emitted)
          <step>.validations.json  Validation results (if ::daggle-validation:: emitted)
  cache/
    <dag-name>/
      <step-id>/
        <cache-key>.json       Cached outputs and artifact hashes
  proc/
    scheduler.pid              Scheduler process ID file
```

- **events.jsonl** — append-only, one JSON object per line, thread-safe via mutex. 13 event types including `run_annotated` (free-form notes attached via `daggle annotate`). `step_completed` carries `peak_rss_kb`, `user_cpu_sec`, and `sys_cpu_sec` sourced from `syscall.Rusage` in platform-tagged adapters (`internal/executor/rusage_{linux,darwin,other}.go`) — macOS `Maxrss` is normalized from bytes to KB for cross-platform comparability
- **meta.json** — written at run start (DAG hash, R version, renv detection), updated at run end (status, end time)
- **Run IDs** — xid-based, globally unique, time-sortable
- **`<step>.sessioninfo.json`** — written only when an R step fails. The R `tryCatch` wrapper in `internal/executor/rscript.go` captures `sessionInfo()`, R version, platform, error message, and timestamp before re-raising the error. Required for compliance workflows that need to prove which package versions were active at the moment of failure

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

## Notification dispatch

Phase 8 added a first-class notification path independent of R. The `internal/notify/` package dispatches messages directly from Go over `net/http` (Slack, ClickUp, generic webhooks) or `net/smtp` (email), so notifications work even when R is unavailable or the DAG has no R steps.

- Channels are defined under the top-level `notifications:` section in `~/.config/daggle/config.yaml` as named entries with a `type` (`slack`, `clickup`, `http`, `smtp`) and type-specific fields
- Hooks reference a channel by name via `notify: <channel-name>` (alongside the existing `r_expr`/`command` forms; exactly one must be set)
- `engine.dispatchNotify` fans out the hook to `internal/notify.Send` with the default message (DAG name + status) or a hook-supplied `message:` override
- Unknown channel names are rejected at DAG parse time by `dag.validateHooks`, so typos fail fast

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

## Companion R package

The `daggleR` package ([cynkra/daggleR](https://github.com/cynkra/daggleR)) is a thin R wrapper around the daggle protocol. It lives in a separate repository and has no build-time dependency on the Go codebase.

**In-step helpers** (base R, no network) run inside R steps executed by daggle. They read/write the same env vars and stdout markers that daggle uses natively:
- `daggle_output()`, `daggle_run_id()`, `daggle_dag_name()`, `daggle_run_dir()`, `daggle_get_output()`, `daggle_get_matrix()`
- Phase 7 additions: `daggle_summary_md()`, `daggle_meta_numeric()`, `daggle_meta_text()`, `daggle_meta_table()`, `daggle_meta_image()`, `daggle_validation()`

**API wrappers** (httr2) talk to the REST API served by `daggle serve --port`:
- DAG management: `daggle_list_dags()`, `daggle_get_dag()`, `daggle_plan()`
- Run management: `daggle_trigger()`, `daggle_list_runs()`, `daggle_get_run()`, `daggle_get_outputs()`, `daggle_get_step_log()`, `daggle_cancel_run()`, `daggle_compare_runs()`
- Artifacts & metadata: `daggle_list_artifacts()`, `daggle_get_summaries()`, `daggle_get_metadata()`, `daggle_get_validations()`
- Approval: `daggle_approve()`, `daggle_reject()`
- Operations: `daggle_health()`, `daggle_cleanup()`

## Design principles

1. **R is the default** — every step is assumed to be R unless stated otherwise; shell is the escape hatch
2. **No database** — files are the database; JSONL events, flat logs, XDG paths
3. **No embedded R** — daggle supervises `Rscript` processes, never links against R
4. **Single binary** — `go build` produces one static binary; R is the only prerequisite
5. **Minimal dependencies** — 5 Go packages; no runtime services (no Redis, no Postgres)
6. **Fail loud** — strict YAML parsing (`KnownFields`), R errors surfaced in status, clear messages for missing packages
