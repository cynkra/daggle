# Penguin Data Quality Monitor

A scheduled pipeline that validates data quality: checks completeness, detects outliers, verifies species balance, and appends results to a running log. Demonstrates cron scheduling, parallel validation checks, failure hooks, and DAG-level ownership metadata (`owner`, `team`, `tags`, `exposures`) for team workflows.

## DAG structure

```
load → check-completeness ─┐
     → check-distributions ─┴→ report
```

Both quality checks run in parallel. If completeness drops below 90%, the pipeline fails and triggers the `on_failure` hook.

## Requirements

- R with `palmerpenguins` (auto-installed if missing)

## Run manually

```bash
cd examples/penguin-monitor
daggle run pipeline.yaml
```

## Run on schedule

The pipeline includes `trigger: { schedule: "@every 6h" }`. Start the scheduler to run it automatically:

```bash
cd examples/penguin-monitor
daggle serve
```

## Output

- `reports/quality_log.csv` — Append-only log of quality check results (one row per run)
- Each run also records completeness percentage and outlier count via inter-step data passing

## Post-mortem and impact

When an alert fires, attach context for the next on-call engineer:

```bash
daggle annotate penguin-monitor latest "Upstream feed was stale — retried at 09:15"
daggle why penguin-monitor  # failure diagnostic
```

List downstream consumers before changing the pipeline:

```bash
daggle impact penguin-monitor
```
