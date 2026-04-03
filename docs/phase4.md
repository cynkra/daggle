# Phase 4 — Power Features & Advanced Steps

Phase 4 adds new R step types, workflow features (approval gates, conditional steps, matrix runs, sub-DAG composition), CLI tools, and foundational schema changes for future evolution.

## What was built

### Foundational changes

- **Schema version** — `version:` field in DAG YAML (default "1"), `v:` field in JSONL events
- **R version enforcement** — `r_version: ">=4.1.0"` constraint with warn-by-default, `r_version_strict: true` to fail
- **`error_on:` field** — Step-level error sensitivity: `error` (default), `warning`, `message`. Wraps R code in `withCallingHandlers()` for all R executors
- **`base.yaml` shared defaults** — Shallow merge of env, hooks, default timeout/retry across DAGs in the same directory
- **renv.lock hash** — Stored in meta.json when renv is detected
- **DAG hash in status** — `daggle status` shows first 12 chars of DAG file hash

### New step types

| Type | YAML field | What it does |
|------|-----------|----------|
| Approval gate | `approve:` | Pauses execution until human approves via `daggle approve` |
| Sub-DAG | `call:` | Executes another DAG as a sub-step |
| Pin | `pin:` | Publishes data/models via `pins::pin_write()` |
| Vetiver | `vetiver:` | MLOps model versioning (`pin`) and deployment (`deploy`) |
| Shiny test | `shinytest:` | Runs `shinytest2::test_app()` |
| Pkgdown | `pkgdown:` | Builds package website via `pkgdown::build_site()` |
| Install | `install:` | Installs packages via `pak` or `install.packages()` |
| Targets | `targets:` | Runs `targets::tar_make()` pipeline |
| Benchmark | `benchmark:` | Runs bench scripts from a directory |
| Revdepcheck | `revdepcheck:` | Runs `revdepcheck::revdep_check()` |

### Workflow features

- **Conditional steps** (`when:`) — Skip steps based on R expression or shell command result
- **Preconditions** — Health checks before expensive steps; fail if not met
- **Matrix runs** — Expand a step into a parameter grid with parallel instances
- **Human-in-the-loop approval** — `approve:` step type with `daggle approve`/`daggle reject` CLI, notify hooks, timeout support
- **Sub-DAG composition** — `call:` step executes another DAG recursively
- **DAG templates** — `daggle init pkg-check|pkg-release|data-pipeline`

### CLI additions

| Command | Description |
|---------|-------------|
| `daggle history <dag>` | Show run history with status and DAG hash |
| `daggle stats <dag>` | Duration trends (avg/p50/p95 per step), success rate |
| `daggle cancel <dag>` | Cancel an in-flight run |
| `daggle clean --older-than <dur>` | Remove old run directories and logs |
| `daggle approve <dag>` | Approve a waiting run |
| `daggle reject <dag>` | Reject a waiting run |
| `daggle init <template>` | Generate a DAG from a built-in template |

## New files

```
cli/approve.go           — daggle approve and daggle reject commands
cli/cancel.go            — daggle cancel command
cli/clean.go             — daggle clean command
cli/history.go           — daggle history command
cli/stats.go             — daggle stats command
cli/init.go              — daggle init with built-in templates
dag/matrix.go            — Matrix expansion logic
executor/approve.go      — Approval gate executor (polls for approval events)
executor/call.go         — Sub-DAG executor (invokes daggle recursively)
executor/pin.go          — pins package executor
executor/vetiver.go      — vetiver package executor
```

## Modified files

```
dag/dag.go               — Version, RVersion, ErrorOn, When, Preconditions, Matrix,
                           Approve, Call, Pin, Vetiver, Shinytest, Pkgdown, Install,
                           Targets, Benchmark, Revdepcheck fields
dag/validate.go          — Validation for all new fields and step types
dag/parse.go             — BaseDefaults loading and merging
cli/run.go               — R version checking, base.yaml loading, renv.lock hashing
cli/status.go            — DAG hash display, approval status display
engine/engine.go         — Matrix expansion, conditional steps, preconditions,
                           cancel checking between tiers
executor/executor.go     — wrapErrorOn helper, factory routing for new types
executor/rpkg.go         — R code generation for 6 new actions
executor/script.go       — error_on wrapper for script steps
executor/inline.go       — error_on wrapper for inline R steps
executor/rmd.go          — error_on wrapper for R Markdown steps
executor/connect.go      — error_on wrapper for Connect deployment
executor/validate.go     — error_on wrapper for validation scripts
state/event.go           — Version field, approval event types (waiting/approved/rejected)
state/run.go             — "waiting" status detection for approval gates
state/meta.go            — RenvLockHash field
```
