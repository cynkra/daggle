# Phase 5 — REST API & Companion R Package

Phase 5 makes daggle programmable from R. Phase 5a adds a REST API to the scheduler daemon. Phase 5b delivers the `daggleR` companion package that wraps the API and the in-step protocol for ergonomic R access.

## Phase 5a — REST API

### What was built

A REST API server embedded in `daggle serve`. When started with `--port`, it exposes 15 endpoints for DAG management, run triggering, step monitoring, approval gates, and maintenance — all designed for R-friendly consumption.

#### Server architecture

- **Package:** `api/` (6 source files + tests)
- **Router:** `net/http` with path-based routing, no external framework
- **Lifecycle:** Runs alongside the scheduler in `daggle serve --port 8787`; shuts down gracefully with the daemon
- **Response format:** All list endpoints return flat JSON arrays (no nesting) so `jsonlite::fromJSON()` or `httr2::resp_body_json(simplifyVector = TRUE)` gives a data.frame directly

#### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/health` | Server health check (status, version, uptime) |
| GET | `/api/v1/openapi.json` | OpenAPI 3.0 specification |
| GET | `/api/v1/dags` | List all DAGs with step count, schedule, last run status |
| GET | `/api/v1/dags/{name}` | DAG definition and latest run status |
| POST | `/api/v1/dags/{name}/run` | Trigger a run (async, returns run_id) |
| POST | `/api/v1/dags/{name}/validate` | Validate a DAG without running |
| GET | `/api/v1/dags/{name}/runs` | List runs for a DAG |
| GET | `/api/v1/dags/{name}/runs/{run_id}` | Run detail with step statuses |
| GET | `/api/v1/dags/{name}/runs/latest` | Alias for most recent run |
| POST | `/api/v1/dags/{name}/runs/{run_id}/cancel` | Cancel an in-flight run |
| GET | `/api/v1/dags/{name}/runs/{run_id}/steps` | List step statuses |
| GET | `/api/v1/dags/{name}/runs/{run_id}/steps/{step_id}/log` | Step stdout/stderr |
| POST | `/api/v1/dags/{name}/runs/{run_id}/steps/{step_id}/approve` | Approve a waiting step |
| POST | `/api/v1/dags/{name}/runs/{run_id}/steps/{step_id}/reject` | Reject a waiting step |
| GET | `/api/v1/dags/{name}/runs/{run_id}/outputs` | All step outputs (flat) |
| POST | `/api/v1/runs/cleanup` | Remove old run data |

#### Design decisions

- **Flat responses** — List endpoints return arrays of objects, not nested structures. This means R users get data frames without post-processing.
- **"latest" alias** — Use `latest` as a run_id to get the most recent run without querying the list first.
- **Async triggers** — `POST /run` returns immediately with a run_id. Poll status via GET.
- **No authentication** — Localhost-only by design. Authentication deferred to Phase 7.
- **No pagination** — daggle manages small, local datasets. Pagination can be added later if needed.
- **Secret redaction** — Secret values from `${vault:}`, `${file:}`, and `secret: true` env vars are never exposed in API responses.

#### Source files

| File | Purpose |
|------|---------|
| `api/server.go` | HTTP server setup, routing, middleware, lifecycle |
| `api/types.go` | Response structs with JSON tags |
| `api/dags.go` | DAG list, detail, validate, trigger handlers |
| `api/runs.go` | Run list, detail, cancel, cleanup handlers |
| `api/steps.go` | Step list, log, approve, reject handlers |
| `api/outputs.go` | Output collection and flat formatting |
| `api/server_test.go` | Handler tests |

---

## Phase 5b — Companion R Package (daggleR)

### What was built

The [`daggleR`](https://github.com/cynkra/daggleR) package — a thin R wrapper around the daggle protocol. It lives in a separate repository (`cynkra/daggleR`) and has no build-time dependency on the Go codebase.

Three categories of functions, organized into 3 source files by category:

### In-step helpers (`R/in-step.R`)

Base R only, no network calls. Used inside R steps run by daggle.

| Function | What it does |
|----------|-------------|
| `daggle_output(name, value)` | Emits `::daggle-output name=<key>::<value>` to stdout for downstream steps |
| `daggle_run_id()` | Reads `DAGGLE_RUN_ID` env var |
| `daggle_dag_name()` | Reads `DAGGLE_DAG_NAME` env var |
| `daggle_run_dir()` | Reads `DAGGLE_RUN_DIR` env var |
| `daggle_get_output(step, key)` | Reads `DAGGLE_OUTPUT_<STEP>_<KEY>` env var |

`daggle_output()` validates the key name against the protocol regex (`[a-zA-Z_][a-zA-Z0-9_]*`) and returns the value invisibly for pipe-friendly use.

**Before daggleR:**
```r
cat(sprintf("::daggle-output name=row_count::%d\n", nrow(data)))
n <- as.integer(Sys.getenv("DAGGLE_OUTPUT_EXTRACT_ROW_COUNT"))
```

**With daggleR:**
```r
daggleR::daggle_output("row_count", nrow(data))
n <- daggleR::daggle_get_output("extract", "row_count")
```

### API wrappers (`R/api.R`)

Require `httr2`. Talk to the REST API served by `daggle serve --port`.

| Function | Endpoint | Returns |
|----------|----------|---------|
| `daggle_list_dags()` | `GET /dags` | data.frame |
| `daggle_get_dag(name)` | `GET /dags/{name}` | list |
| `daggle_trigger(name, params)` | `POST /dags/{name}/run` | list (run_id, status) |
| `daggle_list_runs(name)` | `GET /dags/{name}/runs` | data.frame |
| `daggle_get_run(name, run_id)` | `GET /dags/{name}/runs/{run_id}` | list |
| `daggle_get_outputs(name, run_id)` | `GET /dags/{name}/runs/{run_id}/outputs` | data.frame |
| `daggle_get_step_log(name, run_id, step_id)` | `GET .../steps/{step_id}/log` | list (stdout, stderr) |
| `daggle_cancel_run(name, run_id)` | `POST .../cancel` | list |
| `daggle_approve(name, run_id, step_id)` | `POST .../approve` | list |
| `daggle_reject(name, run_id, step_id)` | `POST .../reject` | list |
| `daggle_health()` | `GET /health` | list |
| `daggle_cleanup(older_than)` | `POST /runs/cleanup` | list |

All API functions resolve the base URL in order: explicit `base_url` parameter > `DAGGLE_API_URL` env var > `http://127.0.0.1:8787`. List endpoints use `simplifyVector = TRUE` to return data.frames directly.

### Internal utilities (`R/utils.R`)

`resolve_base_url()` — unexported helper for base URL resolution with trailing-slash stripping.

### Dependencies

| Package | Role |
|---------|------|
| `httr2` | HTTP client for API wrappers (Import) |
| `testthat` | Unit tests (Suggests) |
| `withr` | Env var mocking in tests (Suggests) |
| `httptest2` | HTTP response mocking in tests (Suggests) |

### Testing strategy

- **In-step helpers:** Tested with `withr::local_envvar()` for env var mocking and `capture.output()` for stdout verification
- **API wrappers:** Tested with `httptest2` fixture-based HTTP mocking — no running daggle instance needed
- **URL resolution:** Tests cover the 3-tier priority (explicit > env var > default) and trailing slash stripping

### Design decisions

- **Scope boundary** — daggleR never parses YAML, runs DAGs, or manages state. If the R package can't run a DAG, the scope is correct.
- **No binary management** — Unlike the `quarto` R package, daggleR does not wrap or install the daggle binary. Users install daggle separately.
- **httr2, not httr** — Modern pipe-friendly HTTP client with built-in retry and error handling.
- **No @import** — All httr2 calls use the `httr2::` prefix to avoid namespace pollution.
- **Plain functions** — No S4, R5, S7, or R6 classes. Simple exported functions only.
- **GitHub-only** — Not on CRAN. Install via `pak::pak("cynkra/daggleR")`.
