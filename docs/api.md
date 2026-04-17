# REST API

daggle exposes a REST API for triggering runs, checking status, approving gates, and reading outputs. The API runs alongside the scheduler when `daggle serve` is started with the `--port` flag. Opening the port URL in a browser shows a read-only status dashboard.

The API is designed to be wrapped. Build custom dashboards with [daggleR](https://github.com/cynkra/daggleR) + Shiny, or any HTTP client.

## Base URL

```
http://localhost:8787/api/v1
```

Port is configurable via `daggle serve --port 8787`. The API is off by default — it only starts when `--port` is explicitly provided.

## Authentication

No authentication by default (localhost-only). Authentication can be added in a future release.

## Endpoints

### System

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/health` | Server health check |
| GET | `/openapi.yaml` | OpenAPI 3.0 specification |

### DAGs

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/dags` | List all DAGs. Supports `?tag=`, `?team=`, `?owner=` filters (combined with AND) |
| GET | `/api/v1/dags/{name}` | Get DAG definition and latest run status (includes `owner`, `team`, `description`, `tags`, `exposures`) |
| POST | `/api/v1/dags/{name}/run` | Trigger a DAG run (async) |
| GET | `/api/v1/dags/{name}/plan` | Show execution plan with cache status |
| GET | `/api/v1/dags/{name}/impact` | List downstream DAGs + declared exposures |

### Runs

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/dags/{name}/runs` | List runs for a DAG |
| GET | `/api/v1/dags/{name}/runs/compare?run1=X&run2=Y` | Compare two runs |
| GET | `/api/v1/dags/{name}/runs/{run_id}` | Get run detail (`run_id` can be "latest") |
| POST | `/api/v1/dags/{name}/runs/{run_id}/cancel` | Cancel an in-flight run |
| GET | `/api/v1/dags/{name}/runs/{run_id}/stream` | Server-Sent Events stream of run events. `?from=start` (default) replays existing then tails; `?from=end` skips existing |
| GET | `/api/v1/dags/{name}/runs/{run_id}/annotations` | List free-form annotations attached to a run |
| POST | `/api/v1/dags/{name}/runs/{run_id}/annotations` | Attach an annotation. Body: `{note, author?}` |

### Steps & Approval

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/dags/{name}/runs/{run_id}/steps` | List step statuses |
| GET | `/api/v1/dags/{name}/runs/{run_id}/steps/{step_id}/log` | Get step stdout/stderr |
| POST | `/api/v1/dags/{name}/runs/{run_id}/steps/{step_id}/approve` | Approve a waiting step |
| POST | `/api/v1/dags/{name}/runs/{run_id}/steps/{step_id}/reject` | Reject a waiting step |

### Outputs

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/dags/{name}/runs/{run_id}/outputs` | Get all step outputs (flat) |

### Summaries & Metadata

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/dags/{name}/runs/{run_id}/summaries` | Get step summaries (markdown) |
| GET | `/api/v1/dags/{name}/runs/{run_id}/metadata` | Get step metadata entries |

### Validations

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/dags/{name}/runs/{run_id}/validations` | Get validation results for a run |

### Artifacts

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/dags/{name}/runs/{run_id}/artifacts` | List declared artifacts with hashes and sizes |

### Maintenance

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/runs/cleanup` | Remove old run data |

## Response Shapes

All list responses return flat arrays of objects for easy conversion to data frames in R (`jsonlite::fromJSON(url) |> as.data.frame()`).

### DAG List

```json
GET /api/v1/dags?tag=etl&team=data

[
  {
    "name": "daily-etl",
    "steps": 5,
    "schedule": "30 6 * * *",
    "last_status": "completed",
    "last_run": "2026-04-01T06:30:00Z",
    "owner": "alice",
    "team": "data",
    "description": "Nightly ETL for the analytics warehouse",
    "tags": ["etl", "daily", "critical"]
  }
]
```

The `owner`, `team`, `description`, and `tags` fields are present only when declared on the DAG. The `?tag=`, `?team=`, `?owner=` query params filter the list with AND semantics.

### Execution Plan

```json
GET /api/v1/dags/daily-etl/plan

[
  { "step_id": "extract",   "status": "outdated", "reason": "script R/extract.R changed" },
  { "step_id": "transform", "status": "cached" },
  { "step_id": "model",     "status": "outdated", "reason": "upstream extract changed" },
  { "step_id": "report",    "status": "no-cache", "reason": "caching not enabled" }
]
```

### Run Detail

```json
GET /api/v1/dags/daily-etl/runs/abc123

{
  "run_id": "abc123",
  "dag_name": "daily-etl",
  "status": "completed",
  "started": "2026-04-01T06:30:00Z",
  "ended": "2026-04-01T06:30:45Z",
  "duration_seconds": 45.2,
  "dag_hash": "a1b2c3d4e5f6",
  "r_version": "4.4.1",
  "platform": "darwin/arm64",
  "params": { "dept": "sales" },
  "steps": [
    {
      "step_id": "extract",
      "status": "completed",
      "duration_seconds": 10.1,
      "attempts": 1,
      "peak_rss_kb": 128345,
      "user_cpu_sec": 0.92,
      "sys_cpu_sec": 0.08
    }
  ]
}
```

Step summaries include `peak_rss_kb`, `user_cpu_sec`, and `sys_cpu_sec` sourced from `syscall.Rusage` (macOS `Maxrss` is normalized to KB for cross-platform comparability).

### Run Comparison

```json
GET /api/v1/dags/daily-etl/runs/compare?run1=abc123&run2=def456

{
  "outputs_diff": [
    { "step_id": "extract", "key": "row_count", "value1": "1542", "value2": "1587" },
    { "step_id": "model",   "key": "accuracy",  "value1": "0.923", "value2": "0.918" }
  ],
  "duration_diff": {
    "run1_seconds": 252.0,
    "run2_seconds": 238.0,
    "diff_seconds": -14.0
  },
  "meta_diff": {
    "dag_hash1": "a1b2c3d4e5f6",
    "dag_hash2": "d4e5f6a1b2c3",
    "changed": true
  }
}
```

### Outputs (R-friendly flat format)

```json
GET /api/v1/dags/daily-etl/runs/abc123/outputs

[
  { "step_id": "extract", "key": "row_count", "value": "42" },
  { "step_id": "extract", "key": "file_path", "value": "/tmp/data.csv" },
  { "step_id": "model",   "key": "accuracy",  "value": "0.95" }
]
```

### Step Log

```json
GET /api/v1/dags/daily-etl/runs/abc123/steps/extract/log

{
  "step_id": "extract",
  "stdout": "Loading data...\nProcessed 42 rows\n",
  "stderr": ""
}
```

### Trigger Run

```json
POST /api/v1/dags/daily-etl/run
Body: { "params": { "dept": "marketing" } }

Response: { "run_id": "abc123", "status": "started" }
```

### Artifacts (R-friendly flat format)

```json
GET /api/v1/dags/daily-etl/runs/abc123/artifacts

[
  {
    "step_id": "extract",
    "name": "raw_data",
    "path": "output/raw.parquet",
    "abs_path": "/home/user/project/output/raw.parquet",
    "hash": "a1b2c3d4e5f6...",
    "size": 1048576,
    "format": "parquet"
  }
]
```

### Validations (R-friendly flat format)

Steps can emit validation results via stdout markers:

```
::daggle-validation status=pass name=row_count::Expected > 0, got 1542
::daggle-validation status=warn name=missing_pct::12% missing (threshold: 20%)
::daggle-validation status=fail name=schema::Column 'date' expected date, got character
```

```json
GET /api/v1/dags/daily-etl/runs/abc123/validations

[
  { "step_id": "extract", "name": "row_count", "status": "pass", "message": "Expected > 0, got 1542" },
  { "step_id": "extract", "name": "missing_pct", "status": "warn", "message": "12% missing (threshold: 20%)" },
  { "step_id": "transform", "name": "schema", "status": "fail", "message": "Column 'date' expected date, got character" }
]
```

A validation with `status=fail` will cause the step to fail (exit code 0 but treated as error) when the step's `error_on` is set to `"error"` or is left at the default.

### Summaries (R-friendly flat format)

Steps can emit summaries via stdout markers:

```
::daggle-summary format=markdown::This is a *summary*
```

```json
GET /api/v1/dags/daily-etl/runs/abc123/summaries

[
  {
    "step_id": "model",
    "format": "markdown",
    "content": "This is a *summary*"
  }
]
```

### Metadata (R-friendly flat format)

Steps can emit structured metadata via stdout markers:

```
::daggle-meta type=numeric name=row_count::1542
::daggle-meta type=text name=model_desc::Linear regression
::daggle-meta type=table name=top5::[{"x":1},{"x":2}]
::daggle-meta type=image name=residuals::output/residuals.png
```

```json
GET /api/v1/dags/daily-etl/runs/abc123/metadata

[
  { "step_id": "extract", "name": "row_count", "type": "numeric", "value": "1542" },
  { "step_id": "model",   "name": "model_desc", "type": "text", "value": "Linear regression" }
]
```

### Annotations

```json
GET /api/v1/dags/daily-etl/runs/abc123/annotations

[
  { "timestamp": "2026-04-17T08:45:12Z", "author": "alice", "note": "DB was down — manual restart at 08:30" }
]
```

```json
POST /api/v1/dags/daily-etl/runs/abc123/annotations
Body: { "note": "re-ran after fix", "author": "bob" }

Response: { "status": "ok" }
```

`author` is optional; the server falls back to the configured CLI user when omitted.

### Impact

```json
GET /api/v1/dags/daily-etl/impact

{
  "dag": "daily-etl",
  "downstream_dags": [
    { "name": "weekly-rollup", "project": "analytics", "trigger_on_status": "completed" }
  ],
  "exposures": [
    { "name": "ops-dashboard", "type": "dashboard", "url": "https://dash.example.com/ops", "description": "Main operations dashboard" }
  ]
}
```

### Live Event Stream (SSE)

```
GET /api/v1/dags/daily-etl/runs/abc123/stream

event: message
data: {"type":"step_started","step_id":"extract",...}

event: message
data: {"type":"step_completed","step_id":"extract","peak_rss_kb":128345,...}

event: end
data: {}
```

- `?from=start` (default) replays all existing events then tails for new ones
- `?from=end` skips existing events and only streams new ones
- Terminal events (`run_completed`, `run_failed`) trigger `event: end`
- A 30-minute idle timeout sends `event: timeout`
- The file is polled every 250ms — no websockets, no long-lived state

See [`api/openapi.yaml`](https://github.com/cynkra/daggle/blob/main/api/openapi.yaml) for exhaustive request/response schemas.

### Error Responses

```json
HTTP 404
{ "error": "run abc123 not found for DAG daily-etl" }
```

Status codes: 200 (ok), 201 (created), 400 (bad request), 404 (not found), 409 (conflict), 500 (server error).

## Design Notes

- **No pagination** — daggle is local-first with small datasets. Add `?limit=N` if needed later.
- **"latest" alias** — use `latest` as run_id to get the most recent run.
- **Async triggers** — POST `/run` returns immediately with run_id. Poll status with GET.
- **Flat outputs** — outputs are flat arrays, not nested maps, for R data.frame compatibility.
- **Secret redaction** — secret values are never exposed in API responses.
