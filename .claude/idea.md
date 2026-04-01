
# 🥁 **Rdag** — A Lightweight DAG Scheduler for R
### Design Draft & Architecture Blueprint

---

## 1. The Role Model: What Makes Dagu Great (and What to Steal)
Dagu is a local-first workflow engine — declarative, file-based, self-contained, air-gapped ready.

It's zero-ops: single binary, file-based storage, under 128MB memory footprint.

It runs without requiring databases, message brokers, or code changes to your existing scripts.
The core design principles to carry over:

| Dagu Principle | Rdag Equivalent |
|---|---|
| Single binary, zero deps | Single binary (or minimal install), R is the only prerequisite |
| YAML-based DAG definitions | YAML-based, but with R-native semantics baked in |
| File-based storage (no DB) | File-based: JSONL status, flat-file logs, XDG-compliant layout |
| Built-in Web UI | Lightweight Web UI (Shiny-free — plain HTML/JS served from the binary) |
| Cron scheduling | Cron scheduling with R-aware hooks |
|
Lifecycle hooks (onSuccess, onFailure, onExit), retries with exponential backoff
| Same, but hooks can be R expressions |

---

## 2. Language Choice: The Central Decision

### Recommendation: **Go** (primary) + R as execution target

**Why Go, not R itself?**

| Consideration | Go | R |
|---|---|---|
| **Single binary** | ✅ `go build` → one static binary | ❌ R is interpreted; distributing R as scheduler = shipping R runtime |
| **Concurrency / process management** | ✅ goroutines, `os/exec`, tight process control | ⚠️ `callr`/`processx` work, but R's event loop is single-threaded |
| **Cron/scheduler loop** | ✅ Trivial with `time.Ticker`, goroutines | ⚠️ Possible with `later` package but fragile for a daemon |
| **Web UI server** | ✅ `net/http` in stdlib, embed frontend with `go:embed` | ⚠️ Shiny or `httpuv` — heavy, not suited for a system daemon |
| **Memory footprint** | ✅ 10–30 MB idle | ❌ R session ≥ 40–80 MB even idle |
| **Cross-platform** | ✅ Cross-compile trivially | ⚠️ R installation varies wildly per OS |
| **Dep-free binary** | ✅ Static linking, zero runtime deps | ❌ Always needs R installed |

**How Go talks to R:**
You can write scripts in R and execute them from Go using `os/exec`, and use `rjson` to encode R objects in JSON which you can unmarshal in Go.
This is the simplest, most robust approach — no CGo, no FFI, no Rserve dependency. Just spawn `Rscript`.

### Could you write the *whole* thing in R?
Yes, for a personal tool. But you'd lose:
- The single-binary story (users need R + your R packages installed)
- Robust process supervision (R supervising R is fragile)
- Low memory overhead

**Hybrid compromise:** Write the engine in Go, but allow all *user-facing configuration hooks* to be R expressions.

---

## 3. Architecture Overview

```
┌──────────────────────────────────────────────────────┐
│                    rdag (single binary)               │
├────────────┬────────────┬─────────────┬──────────────┤
│  Scheduler │   Web UI   │  CLI        │  REST API    │
│  (cron     │  (embedded │  (run,      │  (trigger,   │
│   engine)  │   static)  │   status,   │   status,    │
│            │            │   validate) │   cancel)    │
├────────────┴────────────┴─────────────┴──────────────┤
│                    Core Engine                        │
├────────────┬────────────┬─────────────┬──────────────┤
│ DAG Loader │ R Executor │  Dependency │  State Mgr   │
│ (YAML →    │ (Rscript   │  Resolver   │  (JSONL      │
│  DAG graph)│  process   │  (topo sort)│   file-based)│
│            │  manager)  │             │              │
├────────────┴────────────┴─────────────┴──────────────┤
│                   Storage Layer                       │
├────────────┬────────────┬─────────────────────────────┤
│ DAG Files  │ Log Files  │ State Files (run history)   │
│ (YAML)     │ (stdout/   │ (JSONL per run)             │
│            │  stderr)   │                             │
└────────────┴────────────┴─────────────────────────────┘
         │
         ▼
   ┌───────────┐
   │  Rscript   │  ← The ONLY external dependency
   │  (user's R │
   │   install) │
   └───────────┘
```

---

## 4. YAML DAG Spec — R-Native by Default

The key differentiator: **every step is assumed to be R unless stated otherwise.** No `executor: r` boilerplate.

```yaml
# ~/.config/rdag/dags/daily-report.yaml
name: daily-report
schedule: "30 6 * * MON-FRI"
r_version: ">=4.1.0"           # optional: enforce R version
r_libs: "./project_renv"        # optional: renv/library path

# Environment variables available to all steps
env:
  DB_HOST: "postgres.internal"
  REPORT_DATE: "{{ .Today }}"

params:
  - name: department
    default: "sales"

# Lifecycle hooks — can be R expressions!
on_failure:
  r_expr: 'slackr::slackr_msg(paste("DAG failed:", Sys.time()))'
on_success:
  r_expr: 'logger::log_info("Daily report complete")'

# DAG definition
type: graph
steps:
  - id: extract
    script: etl/extract.R         # path to .R file
    args: ["--dept", "{{ .Params.department }}"]
    timeout: 10m
    retry:
      limit: 3
      backoff: exponential

  - id: validate
    r_expr: |                     # inline R expression
      data <- readRDS("data/raw.rds")
      stopifnot(nrow(data) > 0)
      cat(sprintf("Rows: %d\n", nrow(data)))
    depends: [extract]

  - id: transform
    script: etl/transform.R
    depends: [validate]
    output: TRANSFORM_RESULT      # capture stdout into variable

  - id: model
    script: models/fit_model.R
    depends: [transform]
    r_profile: .Rprofile.prod     # custom .Rprofile for this step

  - id: render_report
    rmd: reports/monthly.Rmd      # first-class Rmd/Quarto support!
    params:
      output_dir: "reports/out"
    depends: [model]

  - id: deploy
    command: scp reports/out/*.html server:/var/www/reports/
    depends: [render_report]
    # ↑ escape hatch: plain shell when you need it
```

### Step Types (The R-Specialized Executor Zoo)

| Step Type | Field | What It Does |
|---|---|---|
| **R script** | `script:` | Runs `Rscript <path> [args]` |
| **Inline R** | `r_expr:` | Writes expr to temp `.R` file, runs via `Rscript -e` |
| **Rmd / Quarto** | `rmd:` / `quarto:` | Runs `rmarkdown::render()` or `quarto render` |
| **Shell escape** | `command:` | Plain shell command (for `scp`, `curl`, etc.) |
| **Sub-DAG** | `call:` | Compose other rdag DAGs |

---

## 5. The R Executor: Design Details

This is the heart of the tool. It's **not** about embedding R via CGo. It's about being the best possible *supervisor* of `Rscript` processes.

```
┌─────────────────────────────────────┐
│          R Executor (Go)            │
├─────────────────────────────────────┤
│                                     │
│  1. Resolve R binary                │
│     - RDAG_R_HOME env override      │
│     - which Rscript                 │
│     - Version check (semver)        │
│                                     │
│  2. Construct environment           │
│     - Merge DAG env + step env      │
│     - Set R_LIBS_USER if renv path  │
│     - Inject RDAG_RUN_ID,           │
│       RDAG_STEP_ID, RDAG_DAG_NAME   │
│     - Set R_PROFILE_USER if custom  │
│                                     │
│  3. Spawn Rscript process           │
│     - os/exec.Command("Rscript",    │
│          "--no-save","--no-restore", │
│          scriptPath, args...)        │
│     - Wire stdout/stderr to log     │
│       files AND real-time stream    │
│                                     │
│  4. Monitor                         │
│     - Timeout enforcement (SIGTERM  │
│       → grace period → SIGKILL)     │
│     - Memory limit (optional,       │
│       via cgroups or ulimit)        │
│     - Exit code interpretation      │
│       (0=success, non-zero=fail)    │
│                                     │
│  5. Capture output                  │
│     - Parse stdout for RDAG_OUTPUT  │
│       markers to pass vars between  │
│       steps (like GH Actions)       │
│     - Capture .rds / .csv artifacts │
│                                     │
└─────────────────────────────────────┘
```

### Inter-Step Data Passing

R steps can communicate downstream via a simple convention:

```r
# In step "extract":
cat("::rdag-output name=row_count::", nrow(data), "\n")
# or write to a well-known location:
saveRDS(result, Sys.getenv("RDAG_ARTIFACT_DIR"), "/extract_result.rds")
```

Downstream steps receive these as environment variables or can read artifacts.

---

## 6. Feature Map (Phased)

### Phase 1 — MVP (Parity with "cron + Rscript" but better)
- [ ] YAML DAG parser (Go `gopkg.in/yaml.v3` — stdlib-adjacent)
- [ ] Topological sort / dependency resolver
- [ ] R script executor via `Rscript` subprocess
- [ ] Inline `r_expr:` support
- [ ] Cron scheduler (single goroutine, tick-based)
- [ ] File-based run state (JSONL, XDG-compliant paths)
- [ ] Stdout/stderr capture to log files
- [ ] CLI: `rdag run`, `rdag status`, `rdag validate`, `rdag list`
- [ ] Basic retry with configurable limit
- [ ] Timeout enforcement per step

### Phase 2 — Usable Daily Driver
- [ ] Web UI (embedded static HTML/JS/CSS via `go:embed`)
  - DAG list, run history, real-time log tailing
  - DAG visualization (simple topological layout)
- [ ] REST API for triggering / monitoring
- [ ] `rmd:` / `quarto:` step type
- [ ] `on_success` / `on_failure` / `on_exit` lifecycle hooks (R expressions)
- [ ] Inter-step variable passing (`::rdag-output::` protocol)
- [ ] Email notifications (stdlib `net/smtp`)
- [ ] Exponential backoff retries
- [ ] `renv` integration (auto-detect `renv.lock`, set `R_LIBS_USER`)
- [ ] `r_version:` enforcement

### Phase 3 — Power Features
- [ ] Sub-DAG composition (`call:` step type)
- [ ] Gantt chart execution view in Web UI
- [ ] Parameterized DAGs (trigger with custom params via CLI/API)
- [ ] Conditional steps (`when:` field with R expression evaluation)
- [ ] Matrix runs (run same step across param grid — e.g., per-department)
- [ ] R session pooling (optional: keep warm `Rscript` process for fast inline exprs)
- [ ] Artifact management (track `.rds`, `.csv`, plots per run)
- [ ] Git sync for DAG definitions

### Phase 4 — Enterprise / Scale (if needed)
- [ ] Distributed workers (coordinator/worker model)
- [ ] Queue system with concurrency limits
- [ ] RBAC (file-based, like dagu)
- [ ] Prometheus metrics endpoint
- [ ] SSH remote R execution

---

## 7. File System Layout

```
~/.config/rdag/                    # XDG_CONFIG_HOME
├── config.yaml                    # Global config (R path, defaults)
├── base.yaml                      # Shared DAG defaults
└── dags/                          # DAG definitions
    ├── daily-report.yaml
    └── model-retrain.yaml

~/.local/share/rdag/               # XDG_DATA_HOME
├── runs/                          # Execution history
│   └── daily-report/
│       └── 2026/04/01/
│           └── run_20260401_063000_abc123/
│               ├── status.jsonl   # Lifecycle events
│               ├── extract.stdout.log
│               ├── extract.stderr.log
│               ├── validate.stdout.log
│               ├── artifacts/     # Step outputs (.rds, plots, etc.)
│               │   └── extract_result.rds
│               └── meta.json      # R version, renv hash, timing
└── proc/                          # PID files for running DAGs
```

---

## 8. Global Config

```yaml
# ~/.config/rdag/config.yaml
r:
  path: /usr/bin/Rscript           # override auto-detect
  default_args: ["--no-save", "--no-restore", "--no-site-file"]
  default_libs: null               # global R_LIBS_USER override
  timeout: 30m                     # global default timeout

scheduler:
  poll_interval: 30s
  zombie_detection: 45s
  max_concurrent_dags: 4

web:
  port: 8787                       # a nod to RStudio Server's default
  host: 127.0.0.1

notifications:
  email:
    smtp_host: smtp.example.com
    from: rdag@example.com

logging:
  retention_days: 90
  max_log_size: 50MB
```

---

## 9. Key Design Decisions to Settle

| Decision | Options | Recommendation |
|---|---|---|
| **How to invoke R** | `Rscript` subprocess vs. Rserve vs. embedded (CGo) | **`Rscript` subprocess** — zero coupling, works with any R install, debuggable |
| **Package management** | Ignore / detect renv / detect packrat / own solution | **Auto-detect `renv.lock`**, set `R_LIBS_USER` accordingly |
| **How steps pass data** | Env vars / temp files / stdout markers / .rds artifacts | **Stdout markers + artifact dir** (both; let user choose) |
| **Web UI framework** | Go template SSR / embedded SPA (Svelte/Preact) / none | **Embedded Preact SPA** — tiny bundle, `go:embed`, zero runtime deps |
| **DAG file format** | YAML / TOML / JSON / R list | **YAML** — proven by dagu, familiar to data engineers |
| **State storage** | JSONL files / SQLite / BoltDB | **JSONL files** (Phase 1), optional SQLite upgrade later |
| **R version management** | Ignore / check / integrate rig | **Check & warn** — don't manage installs, just validate |

---

## 10. What NOT to Build (Stay Light)

Staying true to the dagu ethos means aggressive scoping:

- ❌ **No Python/shell-first mentality** — R is the default, shell is the escape hatch
- ❌ **No database** — files are the database
- ❌ **No plugin system** — step types are compiled in (R script, R expr, Rmd, shell, sub-DAG — that's it)
- ❌ **No cloud integrations** — no AWS/GCP SDKs baked in (use R packages for that)
- ❌ **No R package installation** — that's `renv`'s job
- ❌ **No embedded R runtime** — too fragile, too heavy, defeats the purpose
- ❌ **No Shiny dependency** — Web UI is pure HTML/JS served from Go

---

## 11. Go Dependency Budget

Staying minimal means being strict about Go deps:

| Purpose | Library | Why |
|---|---|---|
| YAML parsing | `gopkg.in/yaml.v3` | Stdlib-adjacent, battle-tested |
| CLI framework | `cobra` or just `flag` | `flag` for extreme minimalism; cobra if subcommands grow |
| Cron parsing | `robfig/cron/v3` | ~500 LOC, zero deps of its own |
| HTTP router | `net/http` (stdlib) | Good enough for a local UI |
| Logging | `log/slog` (stdlib, Go 1.21+) | Zero dependency structured logging |
| Embedded assets | `embed` (stdlib) | Web UI files baked into binary |
| Unique IDs | `google/uuid` or `rs/xid` | One tiny dep for run IDs |
| **Total external deps** | | **3–4 packages** |

---

## 12. CLI Sketch

```bash
# Validate a DAG definition
rdag validate dags/daily-report.yaml

# Run a DAG immediately
rdag run daily-report
rdag run daily-report --params department=marketing

# Start the scheduler + web UI (the "dagu start-all" equivalent)
rdag serve

# Start only the scheduler (no UI)
rdag scheduler

# Check status
rdag status daily-report
rdag history daily-report --last 10

# List all DAGs
rdag list

# Stop a running DAG
rdag stop daily-report --run-id abc123

# Show which Rscript will be used
rdag doctor    # checks R version, renv, paths, etc.
```

---

## 13. Why This Is Better Than `cron + Rscript`

| Pain Point | cron + Rscript | rdag |
|---|---|---|
| Dependencies between scripts | Manual, implicit | Explicit DAG with `depends:` |
| Failure visibility | Buried in `/var/log/syslog` | Web UI, structured logs, notifications |
| Retries | DIY in each script | Declarative: `retry: {limit: 3, backoff: exponential}` |
| Timeouts | None (runaway R processes) | Enforced per-step with SIGTERM/SIGKILL |
| Passing data between steps | Temp files with hardcoded paths | `::rdag-output::` markers + artifact dir |
| Rmd/Quarto rendering | Separate cron entry | First-class `rmd:` step type |
| History / audit | None | Full run history with logs & artifacts |
| Parallelism | None (sequential cron) | DAG-aware: independent branches run in parallel |

---

## TL;DR Action Plan

1. **Start with Go.** Single `main.go` → grow into packages.
2. **Build the R executor first** — the `os/exec` → `Rscript` bridge with stdout/stderr capture, timeout, exit code handling. This is your foundation.
3. **Add the YAML DAG loader + topo sort.** Now you can define multi-step R pipelines.
4. **Add the cron scheduler.** Now it runs unattended.
5. **Add the CLI.** Now it's usable.
6. **Add the Web UI.** Now it's lovable.
7. **Ship a single binary.** `go build -o rdag .` — done.

The only thing your users need installed: **R**.