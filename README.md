# rdag

A lightweight DAG scheduler for R. Define multi-step R workflows in YAML, run them with dependency resolution, parallel execution, retries, timeouts, and cron scheduling — all from a single binary.

rdag sits between "cron + Rscript" and heavy workflow engines. No database, no message broker, no code changes to your existing scripts. The only prerequisite is R.

## Install

```bash
go install github.com/schochastics/rdag/cmd/rdag@latest
```

Or build from source:

```bash
git clone https://github.com/schochastics/rdag.git
cd rdag
go build -o rdag ./cmd/rdag/
```

## Quick start

Create a DAG file at `~/.config/rdag/dags/hello.yaml` (or in a `.rdag/` directory in your project):

```yaml
name: hello
params:
  - name: who
    default: world
steps:
  - id: greet
    command: echo "Hello, {{ .Params.who }}!"

  - id: analyze
    r_expr: |
      cat(paste("R version:", R.version.string, "\n"))
      cat(paste("2 + 2 =", 2 + 2, "\n"))
    depends: [greet]

  - id: report
    script: scripts/report.R
    args: ["--output", "results.csv"]
    depends: [analyze]
```

```bash
# Validate the DAG
rdag validate hello

# Run it
rdag run hello -p who=David

# Check results
rdag status hello

# List all DAGs
rdag list
```

## DAG definition

Every step is assumed to be R unless stated otherwise. Three step types are supported:

| Type | Field | What it does |
|------|-------|-------------|
| R script | `script:` | Runs `Rscript --no-save --no-restore <path> [args]` |
| Inline R | `r_expr:` | Writes expression to a temp `.R` file, runs via Rscript |
| Shell | `command:` | Runs via `sh -c` (escape hatch for non-R tasks) |

### Full YAML reference

```yaml
name: my-pipeline               # required
schedule: "30 6 * * MON-FRI"    # optional cron schedule for rdag serve
workdir: /opt/projects/etl      # optional working directory override

env:                            # environment variables for all steps
  DB_HOST: "localhost"
  REPORT_DATE: "{{ .Today }}"

params:                         # parameterized DAGs
  - name: department
    default: "sales"

# Lifecycle hooks (R expressions or shell commands)
on_success:
  r_expr: 'logger::log_info("Pipeline complete")'
on_failure:
  command: echo "Pipeline failed!" | mail -s "Alert" team@example.com
on_exit:
  r_expr: 'logger::log_info("Pipeline finished")'

steps:
  - id: extract                 # unique step identifier (required)
    script: etl/extract.R       # R script path (relative to workdir)
    args: ["--dept", "{{ .Params.department }}"]
    timeout: 10m                # kill step after this duration
    retry:
      limit: 3                 # retry up to 3 times on failure
    env:                        # step-level env vars
      BATCH_SIZE: "1000"

  - id: validate
    r_expr: |                  # inline R code
      data <- readRDS("data/raw.rds")
      stopifnot(nrow(data) > 0)
      cat("::rdag-output name=row_count::", nrow(data), "\n")
    depends: [extract]          # runs after extract completes
    on_failure:
      r_expr: 'warning("Validation failed")'

  - id: report
    script: etl/report.R
    args: ["--rows", "$RDAG_OUTPUT_VALIDATE_ROW_COUNT"]
    depends: [validate]
    workdir: /tmp/reports        # step-level workdir override

  - id: deploy
    command: scp output.csv server:/data/
    depends: [report]
```

### Template variables

DAG files support Go `text/template` expressions:

| Variable | Example | Description |
|----------|---------|-------------|
| `{{ .Today }}` | `2026-04-01` | Current date (YYYY-MM-DD) |
| `{{ .Now }}` | `2026-04-01T08:30:00Z` | Current time (RFC3339) |
| `{{ .Params.name }}` | `sales` | Parameter value (from defaults or `--param` overrides) |
| `{{ .Env.KEY }}` | `localhost` | DAG-level environment variable |

### Inter-step data passing

Steps can pass data to downstream steps using stdout markers:

```r
# In step "extract":
cat("::rdag-output name=row_count::", nrow(data), "\n")
cat("::rdag-output name=output_path::", path, "\n")
```

Downstream steps receive these as environment variables, namespaced by step ID:

```yaml
- id: report
  # Available as $RDAG_OUTPUT_EXTRACT_ROW_COUNT and $RDAG_OUTPUT_EXTRACT_OUTPUT_PATH
  r_expr: |
    rows <- as.integer(Sys.getenv("RDAG_OUTPUT_EXTRACT_ROW_COUNT"))
    cat(sprintf("Processing %d rows\n", rows))
  depends: [extract]
```

Output markers are parsed and stripped from terminal output but kept in log files.

### Lifecycle hooks

Hooks run after DAG or step completion. They can be R expressions or shell commands:

```yaml
# DAG-level hooks
on_success:
  r_expr: 'slackr::slackr_msg("Pipeline succeeded")'
on_failure:
  command: echo "FAILED" >> /var/log/alerts.log
on_exit:
  r_expr: 'logger::log_info("Done")'

steps:
  - id: model
    script: fit.R
    # Step-level hooks
    on_success:
      r_expr: 'saveRDS(Sys.time(), "last_success.rds")'
    on_failure:
      r_expr: 'saveRDS(last.warning, "debug.rds")'
```

Hooks receive all run metadata and accumulated step outputs as environment variables.

## Cron scheduling

Add a `schedule:` field to any DAG and run `rdag serve` to start the scheduler:

```yaml
name: daily-report
schedule: "30 6 * * MON-FRI"    # 6:30 AM weekdays
steps:
  - id: report
    script: reports/daily.R
```

```bash
rdag serve
```

The scheduler:
- Monitors the DAG directory for files with `schedule:` fields
- Triggers runs on cron expressions (standard 5-field, plus `@every 5m`, `@hourly`, etc.)
- Skips runs if the same DAG is still executing (overlap protection)
- Limits to 4 concurrent DAG runs
- Hot-reloads DAG files every 30 seconds (add, edit, or remove DAGs without restart)
- Shuts down gracefully on SIGINT/SIGTERM (waits for in-flight runs)
- Writes a PID file for process management

## CLI reference

```
rdag run <dag-name> [flags]     Run a DAG immediately
  -p, --param key=value         Override a parameter (repeatable)

rdag validate <dag-name|path>   Validate a DAG definition and show execution plan

rdag status <dag-name> [flags]  Show status of the latest run
  --run-id <id>                 Show a specific run instead

rdag list                       List all available DAGs with last run status

rdag serve                      Start the cron scheduler daemon
```

Global flags:

```
--dags-dir <path>    Override DAG definitions directory
--data-dir <path>    Override data/runs directory
```

## How it works

**Dependency resolution** — Steps declare dependencies via `depends:`. rdag performs a topological sort and groups steps into tiers. Steps within a tier run in parallel.

**Working directory** — Steps execute in the DAG file's directory by default. Override at DAG level (`workdir:`) or step level. Precedence: step > DAG > DAG file directory.

**Timeouts** — Each step can specify a `timeout`. On expiry, rdag sends SIGTERM to the entire process group, waits 5 seconds, then SIGKILL. No orphaned R processes.

**Retries** — Steps with `retry.limit` are re-executed on failure, with linear backoff between attempts.

**Run history** — Every run creates a directory under `~/.local/share/rdag/runs/<dag>/<date>/run_<id>/` containing:
- `events.jsonl` — Lifecycle events (started, completed, failed, retried)
- `<step-id>.stdout.log` / `<step-id>.stderr.log` — Captured output per step

## DAG discovery

rdag looks for DAG files in this order:

1. `--dags-dir` flag (explicit override)
2. `.rdag/` in the current working directory (project-local)
3. `~/.config/rdag/dags/` (global default)

Project-local discovery means DAGs can live alongside the R project they orchestrate:

```
my-project/
  .rdag/
    daily-etl.yaml
    pkg-check.yaml
  R/
    extract.R
    transform.R
  renv.lock
```

## File layout

```
~/.config/rdag/              # XDG_CONFIG_HOME/rdag
  dags/                      # Global DAG definitions

~/.local/share/rdag/         # XDG_DATA_HOME/rdag
  runs/                      # Run history
    my-pipeline/
      2026-04-01/
        run_abc123/
          events.jsonl
          extract.stdout.log
          extract.stderr.log
  proc/
    scheduler.pid            # Scheduler PID file
```

Override with `RDAG_CONFIG_DIR` / `RDAG_DATA_DIR` environment variables or `--dags-dir` / `--data-dir` flags.

## License

MIT
