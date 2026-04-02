# Phase 2 — Usable Daily Driver

Phase 2 adds R-specific step types, better error reporting, Posit Connect deployment, and reproducibility metadata — making daggle a practical daily driver for R package developers and data scientists.

## What was built

### New step types

| Type | YAML field | What it does |
|------|-----------|----------|
| Quarto | `quarto:` | Runs `quarto render <path> [args]` |
| Test | `test:` | Runs `devtools::test()` with structured pass/fail output |
| Check | `check:` | Runs `rcmdcheck::rcmdcheck()` with error/warning/note counts |
| Document | `document:` | Runs `roxygen2::roxygenize()` |
| Lint | `lint:` | Runs `lintr::lint_package()` with issue counts |
| Style | `style:` | Runs `styler::style_pkg()` |
| Deploy | `connect:` | Deploys to Posit Connect (Shiny, Quarto, Plumber) |

All R package dev steps check for required packages and fail with a clear install message if missing. They emit structured output markers (`::daggle-output::`) with counts (test failures, check warnings, lint issues, etc.).

### Exponential backoff retries

The `retry:` block now supports `backoff: exponential` (1s, 2s, 4s, 8s...) and `max_delay:` to cap the wait. Linear backoff remains the default.

### Error reporting

When an R step fails, daggle extracts the R error message from stderr (looking for `Error in ...` or `Error: ...` patterns) and stores it in the event log. `daggle status` now shows the actual R error instead of just "exit status 1".

### Posit Connect deployment

The `connect:` step type deploys content to Posit Connect using the `rsconnect` R package. Supports Shiny apps, Quarto documents, and Plumber APIs. Authentication via `CONNECT_SERVER` and `CONNECT_API_KEY` environment variables.

### Reproducibility metadata

Each run writes `meta.json` containing:
- DAG file SHA-256 hash (for change detection)
- R version (detected via `Rscript --version`)
- Platform (OS/architecture)
- daggle version (embedded via ldflags)
- Parameters used
- Start/end times and final status

### YAML JSON Schema

A JSON Schema at `docs/daggle-schema.json` provides editor autocomplete and validation for DAG YAML files. Works with VS Code (YAML extension) and other schema-aware editors.

## New files

```
executor/quarto.go    — Quarto document/project rendering
executor/rpkg.go      — R package dev steps (test, check, document, lint, style)
executor/connect.go   — Posit Connect deployment
state/meta.go         — Reproducibility metadata (meta.json)
docs/daggle-schema.json — YAML JSON Schema for editor support
```

## Modified files

```
dag/dag.go            — Step struct with new fields (quarto, test, check, document, lint, style, connect), Retry backoff fields, ConnectDeploy struct
dag/validate.go       — Validation for all new step types, connect fields, retry backoff
dag/template.go       — Template expansion for all new fields
dag/parse.go          — HashFile() for DAG version tracking
executor/executor.go  — Factory routing for all new step types, ErrorDetail in Result
executor/process.go   — extractRError() for R error extraction from stderr
engine/engine.go      — retryDelay() with exponential backoff, ErrorDetail in events, meta.json lifecycle
state/event.go        — ErrorDetail field
cli/run.go            — Metadata collection (R version, DAG hash, platform), Version variable
cli/status.go         — Display ErrorDetail for failed steps
.goreleaser.yaml      — Version ldflags
```
