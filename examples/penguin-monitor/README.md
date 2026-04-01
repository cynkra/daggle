# Penguin Data Quality Monitor

A scheduled pipeline that validates data quality: checks completeness, detects outliers, verifies species balance, and appends results to a running log. Demonstrates cron scheduling, parallel validation checks, and failure hooks.

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
rdag run pipeline.yaml
```

## Run on schedule

The pipeline includes `schedule: "@every 6h"`. Start the scheduler to run it automatically:

```bash
cd examples/penguin-monitor
rdag serve
```

## Output

- `reports/quality_log.csv` — Append-only log of quality check results (one row per run)
- Each run also records completeness percentage and outlier count via inter-step data passing
