# rdag

A lightweight DAG scheduler for R. Define multi-step R workflows in YAML, run them with dependency resolution, parallel execution, retries, and timeouts — all from a single binary.

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

Create a DAG file at `~/.config/rdag/dags/hello.yaml`:

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
name: my-pipeline            # required
env:                         # environment variables for all steps
  DB_HOST: "localhost"
  REPORT_DATE: "{{ .Today }}"

params:                      # parameterized DAGs
  - name: department
    default: "sales"

steps:
  - id: extract              # unique step identifier (required)
    script: etl/extract.R    # R script path
    args: ["--dept", "{{ .Params.department }}"]
    timeout: 10m             # kill step after this duration
    retry:
      limit: 3              # retry up to 3 times on failure
    env:                     # step-level env vars
      BATCH_SIZE: "1000"

  - id: validate
    r_expr: |               # inline R code
      data <- readRDS("data/raw.rds")
      stopifnot(nrow(data) > 0)
    depends: [extract]       # runs after extract completes

  - id: deploy
    command: scp output.csv server:/data/
    depends: [validate]
```

### Template variables

DAG files support Go `text/template` expressions:

| Variable | Example | Description |
|----------|---------|-------------|
| `{{ .Today }}` | `2026-04-01` | Current date (YYYY-MM-DD) |
| `{{ .Now }}` | `2026-04-01T08:30:00Z` | Current time (RFC3339) |
| `{{ .Params.name }}` | `sales` | Parameter value (from defaults or `--param` overrides) |
| `{{ .Env.KEY }}` | `localhost` | DAG-level environment variable |

## CLI reference

```
rdag run <dag-name> [flags]     Run a DAG immediately
  -p, --param key=value         Override a parameter (repeatable)

rdag validate <dag-name|path>   Validate a DAG definition and show execution plan

rdag status <dag-name> [flags]  Show status of the latest run
  --run-id <id>                 Show a specific run instead

rdag list                       List all available DAGs with last run status
```

Global flags:

```
--dags-dir <path>    Override DAG definitions directory
--data-dir <path>    Override data/runs directory
```

## How it works

**Dependency resolution** — Steps declare dependencies via `depends:`. rdag performs a topological sort and groups steps into tiers. Steps within a tier run in parallel.

**Timeouts** — Each step can specify a `timeout`. On expiry, rdag sends SIGTERM to the entire process group, waits 5 seconds, then SIGKILL. No orphaned R processes.

**Retries** — Steps with `retry.limit` are re-executed on failure, with a simple linear backoff between attempts.

**Run history** — Every run creates a directory under `~/.local/share/rdag/runs/<dag>/<date>/run_<id>/` containing:
- `events.jsonl` — Lifecycle events (started, completed, failed, retried)
- `<step-id>.stdout.log` / `<step-id>.stderr.log` — Captured output per step

## File layout

```
~/.config/rdag/              # XDG_CONFIG_HOME/rdag
  dags/                      # DAG definitions (YAML files)

~/.local/share/rdag/         # XDG_DATA_HOME/rdag
  runs/                      # Run history
    my-pipeline/
      2026-04-01/
        run_abc123/
          events.jsonl
          extract.stdout.log
          extract.stderr.log
```

Override with `RDAG_CONFIG_DIR` / `RDAG_DATA_DIR` environment variables or `--dags-dir` / `--data-dir` flags.

## License

MIT
