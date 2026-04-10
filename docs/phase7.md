# Phase 7 — Analyst Workflow Features

Phase 7 targets the daily needs of R users doing data analysis, going beyond step types into workflow-level improvements. 11 features across artifacts, caching, rich metadata, data quality, and CLI ergonomics.

## What was built

### Artifact declaration and tracking

Steps declare output files in YAML. After successful execution, daggle verifies each artifact exists, computes SHA-256 hashes and file sizes, and records them as `step_artifact` events.

```yaml
steps:
  - id: extract
    script: R/extract.R
    artifacts:
      - name: raw_data
        path: output/raw.parquet
        format: parquet
      - name: summary_plot
        path: output/summary.png
        format: png
        versioned: true       # append epoch timestamp to filename
```

- Paths are relative to workdir; API returns both relative and absolute paths
- Optional `versioned: true` renames files with epoch timestamp to avoid overwrites
- New API endpoint: `GET /api/v1/dags/{name}/runs/{run_id}/artifacts`

### Step-level caching

Hash step inputs (script content, env vars, upstream outputs, renv.lock hash) and skip execution if unchanged. Opt-in per step.

```yaml
steps:
  - id: extract
    script: R/extract.R
    cache: true
```

- Cache store at `~/.local/share/daggle/cache/<dag>/<step>/<key>.json`
- Cache key: SHA-256 of script content + sorted env vars + upstream output hashes + renv.lock hash
- On hit: replays previous outputs, writes `step_cached` event, skips execution
- `daggle clean --cache` clears the cache
- Thread-safe via `sync.RWMutex` on the cache store

### `daggle plan` command

Dry-run showing which steps would run vs skip when caching is enabled, and why each step is outdated.

```
$ daggle plan my-pipeline
my-pipeline: 4 steps, 2 cached, 2 outdated

STEP          STATUS     REASON
extract       outdated   script R/extract.R changed
transform     cached     -
model         outdated   upstream extract changed
report        no-cache   caching not enabled
```

- Works standalone (no running server required) — reads cache store directly
- Also available as API endpoint: `GET /api/v1/dags/{name}/plan`

### Step summaries and rich metadata

Steps emit structured metadata via stdout markers. Stored in per-step files (not events.jsonl) and exposed via API.

```r
# Markdown summary
cat("::daggle-summary format=markdown::## Results\n- 1542 rows\n")

# Typed metadata
cat("::daggle-meta type=numeric name=row_count::1542\n")
cat("::daggle-meta type=text name=model::linear regression\n")
cat("::daggle-meta type=table name=top5::", jsonlite::toJSON(head(df, 5)), "\n")
cat("::daggle-meta type=image name=residuals::output/residuals.png\n")
```

- Summaries written to `{step}.summary.md`, metadata to `{step}.meta.json`
- Types: `numeric`, `text`, `table` (JSON), `image` (artifact path)
- API endpoints: `GET .../summaries`, `GET .../metadata`

### Data validation protocol

Separate `::daggle-validation::` marker protocol for structured pass/warn/fail results.

```r
cat("::daggle-validation status=pass name=row_count::Expected > 0, got 1542\n")
cat("::daggle-validation status=warn name=missing_pct::12% missing (threshold: 20%)\n")
cat("::daggle-validation status=fail name=schema::Column 'date' expected date, got character\n")
```

- Stored in `{step}.validations.json`
- `status=fail` respects step-level `error_on:` settings
- API endpoint: `GET .../validations`

### Source freshness / data SLAs

Step-level freshness checks on input files. Checked before step execution.

```yaml
steps:
  - id: transform
    script: R/transform.R
    freshness:
      - path: "data/raw.csv"
        max_age: "6h"
        on_stale: warn       # default: fail
```

- Resolves paths relative to workdir, checks file modification time against `max_age`
- Configurable behavior: `on_stale: fail` (default) or `on_stale: warn`

### Deadline / absence alerting

Alert if a scheduled DAG hasn't started by a deadline time.

```yaml
trigger:
  schedule: "30 6 * * MON-FRI"
  deadline: "08:00"
  on_deadline:
    r_expr: 'slackr::slackr_msg("Daily ETL missed its 8am deadline!")'
```

- Scheduler tracks per-DAG daily deadline firing
- Alert only — does not trigger the DAG
- Uses system timezone

### Parameterized reports at scale

`output_dir` and `output_name` template fields for auto-naming rendered reports across matrix parameter sets.

```yaml
steps:
  - id: render
    quarto: reports/monthly.qmd
    matrix:
      client: [acme, globex]
      period: [Q1, Q2]
    output_dir: "reports/{{ .Matrix.client }}/{{ .Matrix.period }}"
    output_name: "{{ .Matrix.client }}_{{ .Matrix.period }}.html"
```

- Quarto executor passes `--output-dir` and `--output` flags
- RMarkdown executor passes `output_dir` and `output_file` to `rmarkdown::render()`
- Matrix template interpolation applied to both fields

### Run comparison / `daggle diff`

Compare outputs, durations, and metadata between two runs.

```
$ daggle diff my-pipeline run_abc run_xyz
STEP       OUTPUT      RUN_ABC    RUN_XYZ    CHANGE
extract    row_count   1542       1587       +45
model      accuracy    0.923      0.918      -0.005

Duration: 4m12s → 3m58s (-14s)
DAG hash: a1b2c3... → d4e5f6... (changed)
```

- API endpoint: `GET .../runs/compare?run1=X&run2=Y`

### Richer notification context

Additional env vars injected into lifecycle hooks:

| Variable | Description |
|----------|-------------|
| `DAGGLE_RUN_DURATION` | Total run duration (e.g. "4m12s") |
| `DAGGLE_RUN_STATUS` | "completed" or "failed" |
| `DAGGLE_FAILED_STEPS` | Comma-separated failed step IDs |

### `daggle logs` command

Direct log access without going through `daggle status`.

```bash
daggle logs my-pipeline                    # all steps, latest run
daggle logs my-pipeline --step extract     # specific step
daggle logs my-pipeline --follow           # tail mode
daggle logs my-pipeline --stderr           # stderr instead of stdout
daggle logs my-pipeline --run-id abc123    # specific run
```

## New API endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/dags/{name}/plan` | Execution plan with cache status |
| GET | `.../runs/{run_id}/artifacts` | Declared artifacts with hashes |
| GET | `.../runs/{run_id}/summaries` | Step markdown summaries |
| GET | `.../runs/{run_id}/metadata` | Typed step metadata |
| GET | `.../runs/{run_id}/validations` | Validation results |
| GET | `.../runs/compare?run1=X&run2=Y` | Run comparison |

## New marker protocols

| Marker | Purpose |
|--------|---------|
| `::daggle-summary format=<fmt>::<content>` | Rich step summaries |
| `::daggle-meta type=<type> name=<name>::<value>` | Typed metadata |
| `::daggle-validation status=<status> name=<name>::<msg>` | Validation results |

Full protocol documentation: [docs/protocols.md](protocols.md).

## New CLI commands

| Command | Description |
|---------|-------------|
| `daggle plan <dag>` | Show cache status per step |
| `daggle diff <dag> <run1> <run2>` | Compare two runs |
| `daggle logs <dag> [--step] [--follow]` | Direct log access |

## New YAML fields

| Field | Level | Description |
|-------|-------|-------------|
| `artifacts:` | step | Declare output files with name, path, format, versioned |
| `cache:` | step | Enable input-hashing cache |
| `freshness:` | step | Source file age checks with on_stale behavior |
| `output_dir:` | step | Output directory template for renders |
| `output_name:` | step | Output filename template for renders |
| `deadline:` | trigger | HH:MM deadline for absence alerting |
| `on_deadline:` | trigger | Hook fired when deadline is missed |

## Source files

### New files

| File | Purpose |
|------|---------|
| `cache/cache.go` | Cache store, key computation, thread-safe operations |
| `cache/cache_test.go` | Cache unit tests |
| `api/artifacts.go` | Artifact listing endpoint |
| `api/summaries.go` | Summary and metadata endpoints |
| `api/validations.go` | Validation results endpoint |
| `cli/plan.go` | `daggle plan` command |
| `cli/diff.go` | `daggle diff` command |
| `cli/logs.go` | `daggle logs` command |
| `dag/load.go` | Shared `LoadAndExpand` helper |
| `state/summary.go` | Shared `BuildStepSummaries` helper |
| `docs/protocols.md` | Marker protocol documentation |

### Modified files

| File | Changes |
|------|---------|
| `dag/dag.go` | Artifact, FreshnessCheck, Cache, OutputDir, OutputName fields |
| `dag/validate.go` | Validation for artifacts, freshness, cache+approve/call |
| `dag/matrix.go` | OutputDir/OutputName template interpolation |
| `engine/engine.go` | Artifact verification, cache logic, freshness checks, hook env vars, decomposed runStep |
| `executor/process.go` | Summary, metadata, validation marker parsing |
| `executor/executor.go` | Summary, MetaEntry, ValidationResult types on Result |
| `executor/quarto.go` | OutputDir/OutputName flag passing |
| `executor/rmd.go` | OutputDir/OutputName parameter passing |
| `scheduler/scheduler.go` | Deadline checking and hook firing |
| `state/event.go` | ArtifactInfo/CacheInfo sub-structs, new event types |
| `api/types.go` | ArtifactEntry, PlanEntry, SummaryEntry, RunMetaEntry, ValidationEntry, CompareResponse |
| `api/server.go` | 6 new route registrations |
| `api/openapi.yaml` | 6 new endpoint specs and schemas |

## Stats

- **+2,087 lines** of Go code added, **-425 lines** removed (net +1,662)
- **11 new files** created
- **6 new API endpoints**, **3 new CLI commands**, **7 new YAML fields**, **3 new marker protocols**
- Released as v0.2.0, followed by v0.2.1 (refactoring)
