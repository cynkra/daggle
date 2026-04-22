# daggle <img src="docs/assets/logo/daggle-hero.png" alt="daggle" width="200" align="right">

[![CI](https://github.com/cynkra/daggle/actions/workflows/ci.yml/badge.svg)](https://github.com/cynkra/daggle/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/cynkra/daggle)](https://goreportcard.com/report/github.com/cynkra/daggle)
[![Go Reference](https://pkg.go.dev/badge/github.com/cynkra/daggle.svg)](https://pkg.go.dev/github.com/cynkra/daggle)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)

> **Experimental.** daggle is under active development. Breaking changes to the YAML schema, CLI, and API may happen frequently until a stable 1.0 release.

A lightweight DAG scheduler for R. Define multi-step R workflows in YAML, run them with dependency resolution, parallel execution, retries, timeouts, and cron scheduling — all from a single binary.

daggle sits between "cron + Rscript" and heavy workflow engines. No database, no message broker, no code changes to your existing scripts. The only prerequisite is R.

## Install

**Install script** (Linux and macOS):

```bash
curl -fsSL https://raw.githubusercontent.com/cynkra/daggle/main/install.sh | sh
```

Set `DAGGLE_INSTALL_DIR` to change the install location (default: `/usr/local/bin`).

**Download binary** from [GitHub Releases](https://github.com/cynkra/daggle/releases) — pre-built for Linux and macOS (amd64 and arm64).

**From source** (requires Go 1.22+):

```bash
go install github.com/cynkra/daggle/cmd/daggle@latest
```

Or clone and build:

```bash
git clone https://github.com/cynkra/daggle.git
cd daggle
go build -o daggle ./cmd/daggle/
```

## Quick start

Create a DAG file at `~/.config/daggle/dags/hello.yaml` (or in a `.daggle/` directory in your project):

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
daggle validate hello

# Run it
daggle run hello -p who=David

# Check results
daggle status hello

# List all DAGs
daggle list
```

## DAG definition

Every step is assumed to be R unless stated otherwise. The following step types are supported:

| Type | Field | What it does |
|------|-------|-------------|
| R script | `script:` | Runs `Rscript --no-save --no-restore <path> [args]` |
| Inline R | `r_expr:` | Writes expression to a temp `.R` file, runs via Rscript |
| Shell | `command:` | Runs via `sh -c` (escape hatch for non-R tasks) |
| Quarto | `quarto:` | Runs `quarto render <path> [args]` |
| Test | `test:` | Runs `devtools::test()` with structured output |
| Check | `check:` | Runs `rcmdcheck::rcmdcheck()` with error/warning/note counts |
| Document | `document:` | Runs `roxygen2::roxygenize()` |
| Lint | `lint:` | Runs `lintr::lint_package()` |
| Style | `style:` | Runs `styler::style_pkg()` |
| R Markdown | `rmd:` | Renders via `rmarkdown::render()` |
| Restore renv | `renv_restore:` | Runs `renv::restore()` to install packages |
| Coverage | `coverage:` | Runs `covr::package_coverage()` with percentage output |
| Validate | `validate:` | Runs a data validation R script via Rscript |
| Approval gate | `approve:` | Pauses execution until human approves via CLI |
| Sub-DAG | `call:` | Executes another DAG as a sub-step |
| Pin | `pin:` | Publishes data/models via `pins::pin_write()` |
| Vetiver | `vetiver:` | MLOps model versioning and deployment |
| Shiny test | `shinytest:` | Runs `shinytest2::test_app()` |
| Pkgdown | `pkgdown:` | Builds package website via `pkgdown::build_site()` |
| Install | `install:` | Installs packages via `pak` or `install.packages()` |
| Targets | `targets:` | Runs `targets::tar_make()` pipeline |
| Benchmark | `benchmark:` | Runs bench scripts from a directory |
| Revdepcheck | `revdepcheck:` | Runs `revdepcheck::revdep_check()` |
| Deploy | `connect:` | Deploys to Posit Connect (Shiny, Quarto, Plumber) |

### Full YAML reference

```yaml
name: my-pipeline               # required
trigger:                        # optional: automated execution triggers
  schedule: "30 6 * * MON-FRI" # cron schedule for daggle serve
workdir: /opt/projects/etl      # optional; defaults to project root (parent of .daggle/)

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
    script: etl/extract.R       # R script path (relative to project root)
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
      cat("::daggle-output name=row_count::", nrow(data), "\n")
    depends: [extract]          # runs after extract completes
    on_failure:
      r_expr: 'warning("Validation failed")'

  - id: report
    script: etl/report.R
    args: ["--rows", "$DAGGLE_OUTPUT_VALIDATE_ROW_COUNT"]
    depends: [validate]
    workdir: reports             # step-level workdir (relative to project root, or absolute)

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
cat("::daggle-output name=row_count::", nrow(data), "\n")
cat("::daggle-output name=output_path::", path, "\n")
```

Downstream steps receive these as environment variables, namespaced by step ID:

```yaml
- id: report
  # Available as $DAGGLE_OUTPUT_EXTRACT_ROW_COUNT and $DAGGLE_OUTPUT_EXTRACT_OUTPUT_PATH
  r_expr: |
    rows <- as.integer(Sys.getenv("DAGGLE_OUTPUT_EXTRACT_ROW_COUNT"))
    cat(sprintf("Processing %d rows\n", rows))
  depends: [extract]
```

Output markers are parsed and stripped from terminal output but kept in log files.

## R package development

daggle has first-class support for R package workflows:

```yaml
name: pkg-check
steps:
  - id: restore
    renv_restore: "."
  - id: document
    document: "."
    depends: [restore]
  - id: lint
    lint: "."
    depends: [document]
  - id: test
    test: "."
    depends: [document]
  - id: coverage
    coverage: "."
    depends: [test]
  - id: check
    check: "."
    depends: [document]
```

Each step checks for its required R package (devtools, rcmdcheck, roxygen2, lintr, styler, covr, renv) and fails with a clear install instruction if missing. Test results, check errors/warnings/notes, lint issue counts, and coverage percentages are emitted as `::daggle-output::` markers for downstream use.

## Triggers

DAGs can be triggered automatically via the `trigger:` block. Run `daggle serve` to start the trigger daemon. Multiple triggers can coexist — any matching trigger starts a run.

| Trigger | Field | Mechanism |
|---------|-------|-----------|
| Cron | `schedule:` | Standard 5-field cron or `@every 5m`, `@hourly` |
| File watcher | `watch:` | fsnotify with debounce |
| Webhook | `webhook:` | HTTP POST with optional HMAC-SHA256 |
| DAG completion | `on_dag:` | Fires when another DAG completes/fails |
| Condition | `condition:` | Polls an R expression or shell command |
| Git | `git:` | Polls for new commits by branch |

```yaml
name: etl-pipeline
trigger:
  schedule: "30 6 * * MON-FRI"   # cron
  watch:                           # also trigger on file drops
    path: /data/incoming/
    pattern: "*.csv"
    debounce: 5s
  overlap: skip                    # skip (default) or cancel running instance
```

## Scheduler

`daggle serve` runs a long-lived daemon managing all triggers. `daggle serve --port 8787` also starts the REST API and a read-only status dashboard. Hot-reloads DAGs every 30s (or immediately on SIGHUP), limits to 4 concurrent runs, and shuts down gracefully on SIGINT/SIGTERM. Without `daggle serve`, use `daggle run` for manual execution.

## REST API

When the scheduler is started with `--port`, it also serves a REST API for programmatic access and a read-only status dashboard. See [docs/api.md](docs/api.md) for the full endpoint reference.

The API is designed to be wrapped — build custom dashboards with daggleR + Shiny, or any HTTP client.

```bash
# List DAGs
curl http://localhost:8787/api/v1/dags

# Trigger a run
curl -X POST http://localhost:8787/api/v1/dags/my-pipeline/run \
  -H 'Content-Type: application/json' \
  -d '{"params": {"dept": "sales"}}'

# Check status
curl http://localhost:8787/api/v1/dags/my-pipeline/runs/latest

# Get outputs (flat format, R-friendly)
curl http://localhost:8787/api/v1/dags/my-pipeline/runs/latest/outputs

# Approve a waiting step
curl -X POST http://localhost:8787/api/v1/dags/my-pipeline/runs/abc123/steps/review/approve
```

All list endpoints return flat JSON arrays for easy conversion to R data frames with `jsonlite::fromJSON()`.

## Companion R package

The [`daggleR`](https://github.com/cynkra/daggleR) package provides in-step helpers and API wrappers for use from R. Install with `pak::pak("cynkra/daggleR")`.

## CLI reference

```
daggle run <dag-name> [flags]     Run a DAG immediately
  -p, --param key=value           Override a parameter (repeatable)

daggle validate <dag-name|path>   Validate a DAG definition and show execution plan

daggle status <dag-name> [flags]  Show status of the latest run
  --run-id <id>                   Show a specific run instead

daggle list                       List all available DAGs with last run status

daggle serve                      Start the scheduler daemon
daggle stop                       Stop the scheduler daemon

daggle doctor                     Check system health (R, renv, scheduler, DAGs)

daggle history <dag-name> [flags] Show run history for a DAG
  --last <N>                      Number of recent runs (default: 10)

daggle stats <dag-name> [flags]   Show duration trends and success rate
  --last <N>                      Number of recent runs to analyze (default: 20)

daggle cancel <dag-name> [flags]  Cancel an in-flight DAG run
  --run-id <id>                   Specific run ID (default: latest)

daggle clean [flags]              Remove old run directories and logs
  --older-than <duration>         Required: e.g. 30d, 24h

daggle approve <dag-name> [flags] Approve a waiting DAG run to continue
  --run-id <id>                   Specific run ID (default: latest)

daggle reject <dag-name> [flags]  Reject a waiting DAG run
  --run-id <id>                   Specific run ID (default: latest)

daggle init <template>            Generate a DAG from a built-in template
                                  Templates: pkg-check, pkg-release, data-pipeline

daggle register [path] [flags]   Register a project for scheduled execution
  --name <name>                   Project name (default: directory basename)

daggle unregister <name|path>    Remove a project from the registry

daggle projects                  List registered projects with DAG counts

daggle version                    Print the daggle version
```

Global flags:

```
--dags-dir <path>    Override DAG definitions directory
--data-dir <path>    Override data/runs directory
```

## Secrets & sensitive environment variables

Environment values can reference external secret sources instead of hardcoding credentials in YAML:

```yaml
env:
  DB_HOST: "postgres.internal"                      # literal value
  DB_PASS: "${env:DATABASE_PASSWORD}"               # read from process environment
  API_KEY: "${file:/run/secrets/api_key}"            # read file contents (trimmed)
  VAULT_SECRET: "${vault:secret/data/myapp#api_key}" # read from HashiCorp Vault KV v2
```

References are resolved at DAG start (fail-fast). Unresolved references (missing env var, missing file, Vault error) fail the DAG before any step runs.

**Vault authentication** uses standard `VAULT_ADDR` and `VAULT_TOKEN` environment variables (falls back to `~/.vault-token`).

**Redaction:** Values from `${vault:}` and `${file:}` sources are automatically redacted (`***`) in JSONL events and `daggle status` output. For `${env:}` values, mark them explicitly:

```yaml
env:
  DB_PASS:
    value: "${env:DATABASE_PASSWORD}"
    secret: true
```

## Approval gates

Pause execution until a human reviews and approves:

```yaml
steps:
  - id: fit-model
    script: models/fit.R
  - id: review
    approve:
      message: "Review model metrics before deploying"
      timeout: 24h
    depends: [fit-model]
  - id: deploy
    connect:
      type: plumber
      path: api/
    depends: [review]
```

```bash
daggle status my-pipeline   # shows "waiting" with approval message
daggle approve my-pipeline  # continue execution
daggle reject my-pipeline   # fail the step
```

## Conditional steps

Skip steps based on conditions:

```yaml
- id: deploy-prod
  script: deploy.R
  when:
    command: 'test "$DAGGLE_ENV" = production'
  depends: [test]
```

## Matrix runs

Run the same step across a parameter grid:

```yaml
- id: fit-model
  script: models/fit.R
  matrix:
    algo: [lm, glm, gam]
    dataset: [train, full]
  args: ["--algo", "{{ .Matrix.algo }}", "--data", "{{ .Matrix.dataset }}"]
```

This expands into 6 parallel step instances, each with `DAGGLE_MATRIX_ALGO` and `DAGGLE_MATRIX_DATASET` environment variables.

## How it works

**Dependency resolution** — Steps declare dependencies via `depends:`. daggle performs a topological sort and groups steps into tiers. Steps within a tier run in parallel.

**Working directory** — Steps execute in the project root (parent of `.daggle/`) by default. Override at DAG level (`workdir:`) or step level. Step-level workdirs can be relative (resolved against the project root or DAG workdir) or absolute. Precedence: step > DAG > project root.

**Timeouts** — Each step can specify a `timeout`. On expiry, daggle sends SIGTERM to the entire process group, waits 5 seconds, then SIGKILL. No orphaned R processes.

**Retries** — Steps with `retry.limit` are re-executed on failure. Supports `backoff: linear` (default) or `backoff: exponential` (1s, 2s, 4s, 8s...) with an optional `max_delay` cap:

```yaml
retry:
  limit: 5
  backoff: exponential
  max_delay: 60s
```

**Error reporting** — When an R step fails, daggle extracts the R error message from stderr and shows it in `daggle status`, so you don't have to dig through log files.

**renv integration** — daggle auto-detects `renv.lock` in the project directory and sets `R_LIBS_USER` so R steps use the project's renv library automatically. If `renv.lock` is found but the library directory is missing, daggle warns you to run `renv::restore()`. To use a custom library path, set `R_LIBS_USER` in the DAG or step `env:` — daggle will not override it.

**Reproducibility** — Each run writes a `meta.json` with the DAG file hash, R version, platform, daggle version, renv detection status, and parameters used. This makes it easy to trace what produced a given result.

**Run history** — Every run creates a directory under `~/.local/share/daggle/runs/<dag>/<date>/run_<id>/` containing:
- `events.jsonl` — Lifecycle events (started, completed, failed, retried)
- `meta.json` — Reproducibility metadata (R version, DAG hash, platform)
- `<step-id>.stdout.log` / `<step-id>.stderr.log` — Captured output per step

## DAG discovery

daggle looks for DAG files in order: `--dags-dir` flag, `.daggle/` in the current directory, `~/.config/daggle/dags/`. Register additional project directories with `daggle register <path>` so the scheduler picks up their DAGs. Steps execute in the project root (parent of `.daggle/`) by default.

## File layout

Config in `~/.config/daggle/`, data in `~/.local/share/daggle/` (XDG-compliant). Override with `DAGGLE_CONFIG_DIR` / `DAGGLE_DATA_DIR` or `--dags-dir` / `--data-dir` flags.

## Testing

Unit tests (fast, no external dependencies):

```bash
go test -race ./...
```

Integration tests live under [internal/integration/](internal/integration/) behind the `integration` build tag and exercise full DAG runs with real `Rscript` / `sh` subprocesses, a running API server, and the scheduler. They require R on `PATH`:

```bash
go test -tags=integration -race ./internal/integration/...
```

CI runs both jobs on every PR; the integration job installs R via `r-lib/actions/setup-r`.

## Editor support

A [JSON Schema](docs/daggle-schema.json) is provided for DAG YAML files. Add this to the top of your YAML files for autocomplete and validation in VS Code (with the YAML extension):

```yaml
# yaml-language-server: $schema=https://github.com/cynkra/daggle/raw/main/docs/daggle-schema.json
```

## License

GPL-3.0 — see [LICENSE](LICENSE) for details.
