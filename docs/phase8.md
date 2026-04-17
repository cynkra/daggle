# Phase 8 — Collaboration & Observability

Phase 8 targets team workflows and production observability. 9 shipped features spanning notifications, DAG metadata, post-mortem support, failure diagnostics, resource profiling, live streaming, a TUI, and impact analysis. Cross-DAG output dependencies (item 9 in the original design) was deferred pending real usage of the artifact system.

## What was built

### Notification channels as first-class config

Named channels in `~/.config/daggle/config.yaml`, referenced from hooks by name. Go dispatches directly — no R required for notifications.

```yaml
# config.yaml
notifications:
  team-slack:
    type: slack
    webhook_url: https://hooks.slack.com/services/...
  ops-email:
    type: smtp
    smtp_host: mail.example.com
    smtp_port: 587
    smtp_from: daggle@example.com
    smtp_to: [ops@example.com]
    smtp_user: daggle
    smtp_password: ${SMTP_PASSWORD}
```

```yaml
# my-dag.yaml
on_failure:
  notify: team-slack
  message: "ETL failed — see ${DAGGLE_RUN_DIR}"   # optional; default uses DAG name + status
```

- Channel types: `slack`, `clickup`, `http` (generic), `smtp`
- Unknown channel names are rejected at DAG parse time
- Hooks must set exactly one of `r_expr`, `command`, or `notify`

Implementation: `internal/notify/`, validated in `dag/validate.go:validateHooks`, dispatched from `engine.dispatchNotify`.

### DAG ownership and annotations

Optional DAG-level metadata for teams with 10+ DAGs.

```yaml
name: etl-nightly
owner: alice
team: data
description: Nightly ETL for the analytics warehouse
tags: [etl, daily, critical]
steps:
  - id: extract
    ...
```

- `daggle list --tag etl --team data --owner alice`
- `GET /api/v1/dags?tag=etl&team=data&owner=alice`
- Both summary and detail API responses include the fields

### Run annotations

Free-form notes attached to runs. Surfaces in status, API, UI.

```
$ daggle annotate etl-nightly 01HK... "DB was down — manual restart at 08:30"
$ daggle status etl-nightly
...
Annotations:
  [2026-04-17 08:45:12] alice: DB was down — manual restart at 08:30
```

- New `run_annotated` event type (`state.EventRunAnnotated`) with `note` and `author`
- Author defaults to `$USER`; `--author` overrides
- `GET /POST /api/v1/dags/{name}/runs/{run_id}/annotations`

### `daggle why` command

Focused failure diagnostic that collapses status + logs + compare into one screen.

```
$ daggle why etl-nightly
DAG: etl-nightly
Run: 01HK...
Status: failed

Failed step: transform
Error: exit status 1
Error detail: Error in dplyr::left_join(...) : 'by' required

Last 20 lines of stderr:
  ...

Step states:
  extract       completed  1.2s
* transform     failed     0.3s
  load          skipped

DAG hash changed since last successful run 01HJ...:
  previous: 8a3fde12bbcd
  current:  2cc0919acae6
```

- Defaults to most recent failed run; accepts explicit run ID
- Compares `dag_hash` against `LatestRunWithStatus(dag, "completed")`
- Helper `state.LatestRunWithStatus(dagName, statuses...)` reusable by other commands

### R sessionInfo() on step failure

Every R step type (`script`, `r_expr`, `quarto`, `rmd`, `rpkg`, `connect`, `pin`, `vetiver`, `validate`, `shinytest`) wraps user R code in a `tryCatch`. On failure, the wrapper writes `{run_dir}/{step_id}.sessioninfo.json` with:

```json
{
  "r_version": "R version 4.5.2 (2025-10-31)",
  "platform": "aarch64-apple-darwin20",
  "error_message": "Error in ... : undefined reference",
  "session_info": "R version 4.5.2 ...\nPlatform: ...\n...",
  "timestamp": "2026-04-17T19:09:36Z"
}
```

The original error is re-raised so the step still fails. On success, no file is written.

Required for compliance: proves which package versions were active at the moment of failure, without a separate `sessionInfo()` call.

### Step-level resource profiling

After `cmd.Wait()`, read `syscall.Rusage` and record peak RSS + CPU time on the `step_completed` event.

```
$ daggle stats etl-nightly
STEP          RUNS  AVG    P50    P95    AVG RSS    P95 RSS    AVG CPU
extract       20    1.2s   1.1s   2.0s   128MB      210MB      0.9s
transform     20    45s    42s    61s    1.2GB      1.5GB      42s
load          20    8s     8s     9s     80MB       95MB       6s
```

- Event fields: `peak_rss_kb`, `user_cpu_sec`, `sys_cpu_sec`
- Linux Maxrss is already KB; macOS Maxrss is bytes — normalized to KB via build-tagged adapters (`rusage_linux.go`, `rusage_darwin.go`, `rusage_other.go`)
- Surfaced in API step summaries and `daggle stats`

### Live event streaming (SSE)

`GET /api/v1/dags/{name}/runs/{run_id}/stream` tails `events.jsonl` and pushes each new event as a Server-Sent Event.

```
event: message
data: {"type":"step_started","step_id":"extract",...}

event: message
data: {"type":"step_completed","step_id":"extract",...}

event: end
data: {}
```

- `?from=start` (default) replays all existing events then tails; `?from=end` skips existing
- Terminal events (`run_completed`, `run_failed`) trigger `event: end`
- 30-minute idle timeout sends `event: timeout`
- Polls the file every 250ms — no websockets, no new deps

### Interactive TUI monitor

```
$ daggle monitor etl-nightly
```

Bubbletea + lipgloss dashboard that streams the SSE endpoint. Shows per-step status, duration, peak RSS, and annotation count. If no daggle server is listening on `http://localhost:8080`, spawns an embedded read-only API on a random port for the session.

- `--run-id` (default `latest`), `--url` (default auto-detect)
- Press `q` / `esc` to quit
- New dependencies: `github.com/charmbracelet/bubbletea`, `github.com/charmbracelet/lipgloss`

### Exposure / impact tracking

DAG-level declaration of downstream consumers. Purely informational — daggle does not deploy or monitor them.

```yaml
name: etl-nightly
exposures:
  - name: ops-dashboard
    type: dashboard
    url: https://dash.example.com/ops
    description: Main operations dashboard
  - name: weekly-email
    type: report
```

- Types: `shiny`, `quarto`, `dashboard`, `report`, `other`
- `daggle impact <dag>` lists downstream DAGs (via `trigger.on_dag.name`) plus declared exposures
- `GET /api/v1/dags/{name}/impact` returns the same data as JSON
- DAG detail endpoint includes `exposures` in its response

## Summary of changes

| Area | Before | After |
|------|--------|-------|
| Event types | 12 | 13 (+`run_annotated`) |
| Event fields on `step_completed` | — | `peak_rss_kb`, `user_cpu_sec`, `sys_cpu_sec` |
| CLI commands | 20 | 23 (+`why`, `annotate`, `monitor`, `impact`) |
| API endpoints | ~25 | +4 (`/impact`, `/annotations` GET/POST, `/stream`) |
| API query params | — | `/dags?tag=&team=&owner=` |
| New YAML fields | — | `owner`, `team`, `description`, `tags`, `exposures`, `on_success.notify` (etc.) |
| Run directory files | — | `{step_id}.sessioninfo.json` on R step failure |
| Config sections | `cleanup`, `tools`, `engine`, `scheduler` | +`notifications` |

## What was deferred

**Cross-DAG output dependencies.** Original design: steps declare `inputs: [other_dag.run_id.output_name]` and daggle resolves them. Deferred until the Phase 7 artifact system is exercised in practice — the spec should follow real usage patterns, not speculation.

## Files changed (summary)

- `state/config.go` — `NotificationChannel` struct and config section
- `dag/dag.go` — `owner`, `team`, `description`, `tags`, `exposures`, `Hook.Notify`, `Hook.Message`
- `dag/validate.go` — `validateHooks`, `validateExposures`
- `state/event.go` — `EventRunAnnotated`, resource fields, `Note`/`Author`
- `state/run.go` — `LatestRunWithStatus`
- `state/summary.go` — resource fields on `StepState`
- `internal/engine/engine.go` — `SetNotifications`, `dispatchNotify`, resource fields on `step_completed`
- `internal/executor/rscript.go` — `wrapRCodeWithSessionInfo`, `rStringLiteral`
- `internal/executor/process.go` — call `extractRusage` after `cmd.Wait`
- `internal/executor/rusage_{linux,darwin,other}.go` — platform Rusage adapters
- `internal/executor/executor.go` — resource fields on `Result`
- `internal/notify/notify.go` — slack/clickup/http/smtp dispatch, `ValidateChannel`
- `internal/cli/annotate.go`, `why.go`, `monitor.go`, `impact.go`, `list.go` — new/updated commands
- `internal/cli/status.go`, `stats.go` — surface annotations + resource metrics
- `internal/tui/model.go` — bubbletea program
- `api/types.go` — `AnnotationEntry`, `AnnotationRequest`, `ExposureEntry`, `DownstreamDAGInfo`, `ImpactResponse`; resource fields on `StepSummary`; new fields on `DAGSummary`/`DAGDetail`
- `api/dags.go` — query-param filtering, `handleGetImpact`, `exposuresFromDAG`
- `api/annotations.go`, `api/stream.go` — new handlers
- `api/steps.go`, `api/server.go` — wire resource fields, register routes
- `api/openapi.yaml` — updated paths, schemas, query params
