# Design

This document explains what daggle is, why it exists, who it's for, and where it's going.

## What is daggle?

daggle is a lightweight DAG scheduler for R. It lets you define multi-step R workflows in YAML and run them with dependency resolution, parallel execution, retries, timeouts, and cron scheduling — all from a single binary.

It sits between "cron + Rscript" and heavy workflow engines like Airflow. No database, no message broker, no code changes to existing scripts. The only prerequisite is R.

## Why does daggle exist?

R has excellent tools for *what* to compute (tidyverse, targets, brms) but poor tools for *when* and *how* to run things reliably:

| Question | Before daggle | With daggle |
|----------|--------------|-------------|
| When should this run? | crontab | `trigger: { schedule: ... }` in YAML |
| What if it fails? | Check syslog manually | Retries with exponential backoff, `on_failure` hooks |
| What if it hangs? | Kill it manually | Timeout with SIGTERM/SIGKILL to entire process group |
| Who gets told? | Nobody | Hooks (R expressions — slackr, blastula, whatever you want) |
| What happened last time? | `cat /var/log/syslog \| grep ...` | `daggle status`, structured events |
| Can independent steps run in parallel? | No | Automatic from DAG structure |
| How do I deploy to Connect? | Manual or custom scripts | `connect:` step type |

## Who is daggle for?

**Primary audience:** R users who run recurring scripts — data scientists, statisticians, R package authors, analysts with scheduled reports.

**Secondary audience:** Teams that deploy R in production — pharma, finance, academic research groups — who need reliability without Airflow-scale complexity.

**Not for:** Python-first data engineering teams (they have Airflow/Dagster), or people who want a visual DAG builder.

## Core design decisions

### Go + Rscript, not pure R

daggle is written in Go for the single-binary story, low memory footprint, robust process supervision, and trivial cross-compilation. R is the *execution target*, not the implementation language. daggle spawns `Rscript` processes and supervises them — no CGo, no FFI, no Rserve.

### YAML, not R code

DAG definitions are YAML because:
- Declarative — easy to read, diff, review
- Language-agnostic tooling — editors, linters, schemas all work
- No R dependency for parsing — the scheduler can read DAGs without loading R

### Files, not databases

All state lives in the filesystem:
- JSONL events (append-only, one JSON object per line)
- Per-step log files (stdout/stderr)
- Metadata JSON (R version, DAG hash, platform)
- XDG-compliant directory layout

This means zero setup, easy backup (`tar` the data directory), and inspectability (`cat events.jsonl | jq`).

### Strict YAML parsing

`KnownFields(true)` — misspelled fields are rejected, not silently ignored. A YAML JSON Schema is provided for editor autocomplete.

### R is the default

Every step is assumed to be R unless stated otherwise. `script:` runs `Rscript`, `r_expr:` writes inline R to a temp file and runs it. Shell (`command:`) is the escape hatch. This means daggle can auto-detect renv, enforce R version constraints, parse R-specific outputs, and provide R-specific step types.

### renv autodetection

daggle automatically detects `renv.lock` in the project directory (the DAG's working directory or the directory containing the YAML file). When found, it resolves the renv library path (`renv/library/R-<major.minor>/<platform>/`) and injects `R_LIBS_USER` into the environment for all R steps.

This means R steps use the project's renv library without any configuration:

```
my-project/
  .daggle/pipeline.yaml
  renv.lock
  renv/library/R-4.4/aarch64-apple-darwin20/
  scripts/analysis.R
```

Behavior:
- If `renv.lock` exists and the library directory is present, `R_LIBS_USER` is set automatically
- If `renv.lock` exists but the library directory is missing, a warning is printed (run `renv::restore()` first)
- If the user sets `R_LIBS_USER` explicitly in the DAG or step `env:`, daggle does not override it
- renv detection is recorded in `meta.json` for reproducibility

## Step types

| Type | Field | What it does |
|------|-------|-------------|
| R script | `script:` | `Rscript --no-save --no-restore <path> [args]` |
| Inline R | `r_expr:` | Writes to temp `.R` file, runs via Rscript |
| Shell | `command:` | `sh -c <command>` |
| Quarto | `quarto:` | `quarto render <path> [args]` |
| Test | `test:` | `devtools::test()` with structured output |
| Check | `check:` | `rcmdcheck::rcmdcheck()` with error/warning/note parsing |
| Document | `document:` | `roxygen2::roxygenize()` |
| Lint | `lint:` | `lintr::lint_package()` with issue counts |
| Style | `style:` | `styler::style_pkg()` |
| R Markdown | `rmd:` | `rmarkdown::render()` |
| Restore renv | `renv_restore:` | `renv::restore()` to install packages |
| Coverage | `coverage:` | `covr::package_coverage()` with percentage output |
| Validate | `validate:` | Run a data validation R script via Rscript |
| Deploy | `connect:` | Deploy to Posit Connect (Shiny, Quarto, Plumber) |

R-specific steps check for their required packages at runtime and fail with a clear install instruction if missing.

## Roadmap

### Completed

**Phase 1 — MVP:** YAML parsing, topo sort, 3 step types (script/r_expr/command), retries, timeouts, lifecycle hooks, inter-step output passing, cron scheduler, CLI (run/validate/status/list/serve), JSONL events, XDG paths.

**Phase 2 — Usable Daily Driver:** Quarto step type, R package dev steps (test/check/document/lint/style), Posit Connect deployment, exponential backoff retries, R error extraction in status, reproducibility metadata (meta.json), YAML JSON Schema, renv autodetection.

**Phase 3 — Triggers & New Step Types:** Unified trigger block (schedule, file watcher, webhook, DAG completion, condition polling, git), new step types (rmd, renv_restore, coverage, validate), cancellation (daggle stop), overlap policies (skip, cancel), graceful daemon lifecycle, daggle doctor diagnostics.

**Phase 4 — Power Features & Advanced Steps:** New step types (approve, call, pin, vetiver, shinytest, pkgdown, install, targets, benchmark, revdepcheck), conditional steps (when), preconditions, matrix runs, human-in-the-loop approval gates, sub-DAG composition, DAG templates (daggle init), R version enforcement, error_on field, base.yaml defaults, daggle cancel/clean/history/stats commands, duration trends.

**Phase 5a — REST API:** 15 endpoints for DAG management, run triggering/monitoring, step logs/approval, outputs, cleanup. Flat JSON responses for R data.frame compatibility. Runs alongside scheduler via `daggle serve --port`.

**Phase 5b — Companion R Package:** `daggleR` package ([cynkra/daggleR](https://github.com/cynkra/daggleR)). Three categories: in-step helpers (base R only — `output()`, `run_id()`, `dag_name()`, `run_dir()`, `get_output()`), API wrappers (httr2 — `list_dags()`, `get_dag()`, `trigger()`, `list_runs()`, `get_run()`, `get_outputs()`, `get_step_log()`, `cancel_run()`, `health()`, `cleanup()`), and approval helpers (`approve()`, `reject()`). Scope: read, write, approve, and API calls — never parsing YAML or managing state.

**Phase 6 — Minimal Status UI:** Read-only status dashboard embedded in the Go binary via `go:embed`. DAG list, run detail with step status, log viewer. Served alongside the REST API on `daggle serve --port`. No JS framework — Go HTML templates + CSS only. For custom dashboards, use the REST API with daggleR/Shiny or any HTTP client.

**Phase 7 — Analyst Workflow Features:** 11 features targeting daily data analysis needs. Artifact declaration with SHA-256 hashing. Step-level caching (input hashing, `daggle plan` dry-run). Rich metadata protocols (`::daggle-summary::`, `::daggle-meta::`, `::daggle-validation::`). Source freshness checks with `on_stale: fail | warn`. Deadline alerting for scheduled DAGs. Parameterized report rendering (`output_dir`/`output_name` templates). Run comparison (`daggle diff`). Richer hook context env vars. Direct log access (`daggle logs`). 6 new API endpoints, 3 new CLI commands, 7 new YAML fields.

### Planned

**Phase 8 — Workflow Composition & UX:**
- **Cross-DAG output dependencies** — Steps declare inputs from other DAGs' runs. Deferred until the artifact system is proven in practice.
- **Exposure / impact tracking** — Declare downstream consumers (Shiny apps, dashboards) of DAG outputs. `daggle impact <dag>` for dependency analysis.
- **Interactive TUI monitor** — Terminal dashboard for live DAG execution using bubbletea or similar.
- **Migrate to internal/ packages** — Move cli/, executor/, and engine/ to internal/ packages to prevent unintended external imports and clarify the public API surface.

**Phase 9 — Enterprise (if needed):**
- Distributed workers
- Queue system with concurrency limits (including queue overlap policy)
- RBAC
- Prometheus metrics
- SSH remote execution
- R session pooling (keep warm Rscript processes for fast inline expressions)

## Open design questions

These are topics where the design is not yet settled:

- **R version change detection** — R version is recorded in `meta.json`, but if the system R is upgraded between runs, renv library paths may break silently (the platform-specific path like `renv/library/R-4.4/aarch64-apple-darwin20/` changes with R minor version bumps). daggle currently does not warn about this. Suggested direction: add a `daggle doctor` check that compares the current R version against the last recorded version in recent runs. Warn if the R minor version changed and renv is in use.
