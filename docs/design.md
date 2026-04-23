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
| Database | `database:` | SQL query via R DBI, result written to CSV/TSV/RDS/Parquet/Feather |
| Email | `email:` | Send email via a named SMTP channel (Go net/smtp, no R required) |
| Docker | `docker:` | Run a command inside a Docker container (isolation for different R versions or system libs) |

R-specific steps check for their required packages at runtime and fail with a clear install instruction if missing.

## Roadmap

### Completed

**Phase 1 — MVP:** YAML parsing, topo sort, 3 step types (script/r_expr/command), retries, timeouts, lifecycle hooks, inter-step output passing, cron scheduler, CLI (run/validate/status/list/serve), JSONL events, XDG paths.

**Phase 2 — Usable Daily Driver:** Quarto step type, R package dev steps (test/check/document/lint/style), Posit Connect deployment, exponential backoff retries, R error extraction in status, reproducibility metadata (meta.json), YAML JSON Schema, renv autodetection.

**Phase 3 — Triggers & New Step Types:** Unified trigger block (schedule, file watcher, webhook, DAG completion, condition polling, git), new step types (rmd, renv_restore, coverage, validate), cancellation (daggle stop), overlap policies (skip, cancel), graceful daemon lifecycle, daggle doctor diagnostics.

**Phase 4 — Power Features & Advanced Steps:** New step types (approve, call, pin, vetiver, shinytest, pkgdown, install, targets, benchmark, revdepcheck), conditional steps (when), preconditions, matrix runs, human-in-the-loop approval gates, sub-DAG composition, DAG templates (daggle init), R version enforcement, error_on field, base.yaml defaults, daggle cancel/clean/history/stats commands, duration trends.

**Phase 5a — REST API:** 15 endpoints for DAG management, run triggering/monitoring, step logs/approval, outputs, cleanup. Flat JSON responses for R data.frame compatibility. Runs alongside scheduler via `daggle serve --port`.

**Phase 5b — Companion R Package:** `daggleR` package ([cynkra/daggleR](https://github.com/cynkra/daggleR)). All exports are prefixed with `daggle_` to avoid namespace collisions. Three categories: in-step helpers (base R only — `daggle_output()`, `daggle_run_id()`, `daggle_dag_name()`, `daggle_run_dir()`, `daggle_get_output()`), API wrappers (httr2 — `daggle_list_dags()`, `daggle_get_dag()`, `daggle_trigger()`, `daggle_list_runs()`, `daggle_get_run()`, `daggle_get_outputs()`, `daggle_get_step_log()`, `daggle_cancel_run()`, `daggle_health()`, `daggle_cleanup()`), and approval helpers (`daggle_approve()`, `daggle_reject()`). Scope: read, write, approve, and API calls — never parsing YAML or managing state.

**Phase 6 — Minimal Status UI:** Read-only status dashboard embedded in the Go binary via `go:embed`. DAG list, run detail with step status, log viewer. Served alongside the REST API on `daggle serve --port`. No JS framework — Go HTML templates + CSS only. For custom dashboards, use the REST API with daggleR/Shiny or any HTTP client.

**Phase 7 — Analyst Workflow Features:** 11 features targeting daily data analysis needs. Artifact declaration with SHA-256 hashing. Step-level caching (input hashing, `daggle plan` dry-run). Rich metadata protocols (`::daggle-summary::`, `::daggle-meta::`, `::daggle-validation::`). Source freshness checks with `on_stale: fail | warn`. Deadline alerting for scheduled DAGs. Parameterized report rendering (`output_dir`/`output_name` templates). Run comparison (`daggle diff`). Richer hook context env vars. Direct log access (`daggle logs`). 6 new API endpoints, 3 new CLI commands, 7 new YAML fields.

**Phase 8 — Collaboration & Observability:** 9 features for team workflows and production observability. Named notification channels in `config.yaml` (slack, clickup, smtp, generic http) referenced from hooks via `notify: <name>`. DAG-level `owner:`, `team:`, `description:`, `tags:` with filtering in `daggle list` and `GET /dags`. Run annotations (`daggle annotate`, new `run_annotated` event). Focused failure diagnostic (`daggle why`). R `sessionInfo()` auto-capture on step failure (`{step}.sessioninfo.json`). Step-level peak RSS and CPU via `syscall.Rusage`, surfaced in `daggle stats` and the API. Live SSE stream endpoint. Interactive bubbletea TUI (`daggle monitor`). Exposure/impact tracking with `exposures:` field and `daggle impact`. Cross-DAG output dependencies deferred to a later phase.

**Phase 9 — Safety & Compliance:** 4 features targeting compliance and predictable resource use. Immutable run archives (`daggle archive`/`daggle verify`) bundle a run directory into a `.tar.gz` with an embedded SHA-256 manifest that detects tampering, missing files, and extras. DAG change log snapshots the YAML into each run (`dag.yaml`) and writes a unified diff (`dag_diff.patch`) whenever `dag_hash` changes between runs. Dry-run validation (`daggle run --dry-run`) reports what a run would do — secrets resolved, R version, renv, freshness, script existence — without creating a run or executing steps. DAG-level `max_parallel:` caps concurrent step execution via a semaphore.

### Planned

**Phase 10 — Developer Experience & Ecosystem:**
- **`daggle watch`** — Monitor DAG YAML and referenced scripts for changes. On save, re-validate and optionally re-run changed steps only (using cache). Like `nodemon` but DAG-aware.
- **`daggle lint` with editor integration** — Semantic diagnostics beyond YAML validation: do referenced scripts exist? Are required R packages installed? Are secret references resolvable? Output in GNU/JSON format for VS Code integration.
- **`database:` step type** — SQL query as a step. DSN from env vars, query in YAML, output as CSV/RDS artifact. Eliminates the most common R ETL boilerplate (DBI + dbGetQuery + write.csv).
- **`email:` step type** — Send rendered reports and artifacts via email. Uses Go's `net/smtp` directly (no R needed). References notification channel config. Covers the "render and email" workflow declaratively.
- **`docker:` step type** — Run a step inside a Docker container. Isolation for different R versions or system library conflicts (geospatial/GDAL, Bioconductor). Docker is just another subprocess to supervise.

**Phase 11 — Deploy & Secure:**

Today daggle supports exactly one deployment shape: the on-prem PWB sidecar, where daggle runs as a supervisord process inside the PWB container, binds to `127.0.0.1` only (hardcoded), and runs unauthenticated because the port is never exposed outside the container. Phase 11 adds a second supported posture — a small-scale, single-node, self-hosted daggle brought up with `docker compose up` and reachable from a browser with TLS and a login — while leaving the on-prem defaults untouched (loopback, no auth). Inspired by [dagu](https://github.com/dagu-org/dagu)'s deployment model (loopback-by-default, explicit `DAGU_HOST=0.0.0.0`, `DAGU_AUTH_MODE=basic|builtin|oidc`, single-volume file-backed state, published image, minimal + prod compose files).

Scope is deliberately narrow: static bearer token + HTTP basic auth only — no user accounts, no JWT sessions, no `/setup` flow, no OIDC. Multi-user RBAC, SSO, and distributed workers stay in Phase 12. daggle is pre-release, so internal rewrites (pulling transport concerns out of `api/server.go` into an `internal/httpserve/` package, adding an `internal/auth/` middleware layer) are on the table.

- **Configurable bind address** — `--bind` / `DAGGLE_BIND_ADDR`, default `127.0.0.1` (on-prem posture preserved). Opt in to `0.0.0.0` for public bind.
- **Auth modes** — `DAGGLE_AUTH_MODE=none|basic|token`. Basic uses `DAGGLE_AUTH_BASIC_USERNAME/PASSWORD` (or `_PASSWORD_FILE` for Docker secrets). Token uses `DAGGLE_AUTH_TOKEN` / `DAGGLE_AUTH_TOKEN_FILE`, auto-generated and persisted to `$DAGGLE_DATA_DIR/auth/token` (0600) if unset, printed once on first start.
- **TLS termination in daggle** — `DAGGLE_TLS_CERT_FILE` + `DAGGLE_TLS_KEY_FILE` for the no-reverse-proxy case.
- **Reverse-proxy awareness** — `DAGGLE_BASE_PATH` for subpath mounting behind Caddy/nginx; `--trust-proxy` to honor `X-Forwarded-*` headers.
- **Safety guardrails** — daggle refuses to start if bind is non-loopback and auth is `none` (prevents accidental open API); refuses if TLS cert/key are asymmetric; refuses basic mode with missing credentials.
- **First-class Docker** — published `ghcr.io/cynkra/daggle` (~50 MB, HTTP/shell/quarto DAGs) and `ghcr.io/cynkra/daggle-r` (rocker/r-ver + renv, full R step support). GoReleaser publishes both on tag.
- **Compose templates** — `deploy/docker/compose.minimal.yaml` (loopback, no auth, matches on-prem posture) and `deploy/docker/compose.selfhost.yaml` (daggle + Caddy auto-TLS via Let's Encrypt, token auth by default).
- **`daggle token generate`** — print a new secure random token (32 bytes, base64url).
- **`daggle doctor` deploy section** — reports bind, auth mode, TLS state, base path; warns on near-misses of the guardrails.
- **daggleR auth** — companion R package honors `DAGGLE_API_TOKEN` (Authorization: Bearer) and `DAGGLE_API_BASIC_USER`/`DAGGLE_API_BASIC_PASSWORD`. Tracked separately in the daggleR repo.
- **Docs** — new `docs/deploy.md` covers both on-prem PWB sidecar and self-hosted compose. Existing webhook HMAC in `scheduler/webhook.go` is already correct and just needs to be documented.

**Phase 12 — Scale:**
- **State compaction** — `daggle compact <dag>` merges old `events.jsonl` files into summary records. Keep full detail for recent N runs, compress historical data. Prevents slowdown on hourly DAGs running for months.
- **Distributed workers** — Coordinator/worker model for running steps across machines.
- **Queue system** — Concurrency limits with queue overlap policy.
- **RBAC** — File-based role-based access control. Builds on the Phase 11 auth middleware, whose `Validator` interface attaches a principal to the request context so authorization checks layer on without touching transport code.
- **Prometheus metrics** — Expose scheduler and run metrics for monitoring.
- **SSH remote execution** — Run steps on remote machines via SSH.
- **R session pooling** — Keep warm Rscript processes for fast inline expressions.

## Open design questions

These are topics where the design is not yet settled:

- **R version change detection** — R version is recorded in `meta.json`, but if the system R is upgraded between runs, renv library paths may break silently (the platform-specific path like `renv/library/R-4.4/aarch64-apple-darwin20/` changes with R minor version bumps). daggle currently does not warn about this. Suggested direction: add a `daggle doctor` check that compares the current R version against the last recorded version in recent runs. Warn if the R minor version changed and renv is in use.

## Possible Features

Ideas we may want to consider at some point. Not on the roadmap; no commitment to implement.

- **`daggle explain`** — Human-readable prose summary of a DAG: "5 steps, runs weekdays at 6:30am, sends Slack on failure." Useful for onboarding and reviewing unfamiliar DAGs.
