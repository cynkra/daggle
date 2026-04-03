# REST API

daggle exposes a REST API for triggering runs, checking status, approving gates, and reading outputs. The API runs alongside the scheduler when `daggle serve` is started with the `--port` flag.

## Base URL

```
http://localhost:8787/api/v1
```

Port is configurable via `daggle serve --port 8787` (default: 8787).

## Authentication

No authentication by default (localhost-only). Authentication can be added in a future release.

## Endpoints

### System

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/health` | Server health check |
| GET | `/api/v1/openapi.json` | OpenAPI 3.0 specification |

### DAGs

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/dags` | List all DAGs |
| GET | `/api/v1/dags/{name}` | Get DAG definition and latest run status |
| POST | `/api/v1/dags/{name}/run` | Trigger a DAG run (async) |
| POST | `/api/v1/dags/{name}/validate` | Validate a DAG without running |

### Runs

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/dags/{name}/runs` | List runs for a DAG |
| GET | `/api/v1/dags/{name}/runs/{run_id}` | Get run detail with step status |
| GET | `/api/v1/dags/{name}/runs/latest` | Alias for most recent run |
| POST | `/api/v1/dags/{name}/runs/{run_id}/cancel` | Cancel an in-flight run |

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

### Maintenance

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/runs/cleanup` | Remove old run data |

## Response Shapes

All list responses return flat arrays of objects for easy conversion to data frames in R (`jsonlite::fromJSON(url) |> as.data.frame()`).

### DAG List

```json
GET /api/v1/dags

[
  {
    "name": "daily-etl",
    "steps": 5,
    "schedule": "30 6 * * *",
    "last_status": "completed",
    "last_run": "2026-04-01T06:30:00Z"
  }
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
      "attempts": 1
    }
  ]
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
