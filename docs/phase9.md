# Phase 9 — Safety & Compliance

Phase 9 delivers four features focused on compliance, reproducibility, and resource safety: immutable run archives with tamper detection, cross-run DAG change logs, a dry-run validator, and DAG-level parallel execution limits.

## What was built

### Immutable run archives

Bundle a run directory into a tamper-evident `.tar.gz` with an embedded SHA-256 manifest. Detects file corruption, tampering, and out-of-band modifications. FDA 21 CFR Part 11 adjacent.

```
$ daggle archive etl-nightly 01HK... -o /backup/etl-2026-04-21.tar.gz
Archive: /backup/etl-2026-04-21.tar.gz
  files: 14
  bytes: 48293 (uncompressed)

$ daggle verify /backup/etl-2026-04-21.tar.gz
OK: 14 files verified

$ dd if=/dev/zero of=/backup/etl-2026-04-21.tar.gz bs=1 count=1 seek=500 conv=notrunc
$ daggle verify /backup/etl-2026-04-21.tar.gz
FAIL: /backup/etl-2026-04-21.tar.gz
  mismatched (1):
    events.jsonl
```

- Archive layout: a single `.tar.gz` with `.manifest.sha256` as the first entry (lines of `<sha256>  <relative-path>`), followed by the run's files in sorted order.
- Verify reads the manifest, recomputes every hash, and reports mismatches, missing, and extra files separately.
- Deterministic output: files are emitted in sorted relative-path order so two archives of the same run directory produce byte-identical tarballs modulo gzip timestamps.
- Standard library only — no new dependencies.

Implementation: `internal/archive/archive.go`, `internal/archive/verify.go`, CLI in `internal/cli/archive.go` and `internal/cli/verify.go`.

### DAG change log

When a DAG's YAML hash changes between runs, daggle writes a unified diff into the new run directory as `dag_diff.patch`. Self-contained "what changed?" without a git repo.

```
$ daggle run etl-nightly
Starting DAG "etl-nightly" (run 01HK...)
DAG changed since prior run 01HJ... — diff written to dag_diff.patch

$ cat ~/.local/share/daggle/runs/etl-nightly/.../dag_diff.patch
--- etl-nightly/dag.yaml (run 01HJ...)
+++ etl-nightly/dag.yaml (current)
@@ -1,8 +1,10 @@
 name: etl-nightly
 steps:
   - id: extract
     script: R/extract.R
+  - id: validate
+    validate: R/validate.R
   - id: load
     script: R/load.R
```

- Every run now snapshots its DAG YAML into `<run-dir>/dag.yaml` — stable input for future diffs independent of later edits to the source file.
- Diff is written only when `dag_hash` differs from the prior run (any status).
- Runs created before Phase 9 have no `dag.yaml` snapshot; diffing against them is a no-op (logged, not an error).

Implementation: `state/diff.go` (`WriteDAGDiff`), `state/diff_util.go` (LCS-based unified-diff formatter), wired into `internal/cli/run.go` before engine start.

### Dry-run validation

`daggle run --dry-run <dag>` reports what a run *would* do without creating a run directory or executing any steps. Deeper than `daggle plan` (which only checks cache status).

```
$ daggle run etl-nightly --dry-run
Dry-run: etl-nightly (/path/to/etl-nightly.yaml)
================================
[OK]   DAG YAML valid
[OK]   Secrets resolved
[OK]   R version 4.5.2 satisfies >=4.1.0
[OK]   renv library ready: /path/to/renv/library/R-4.5/aarch64-apple-darwin20
[WARN] Freshness: step "extract" source data/raw.csv is 3h old (max 1h)
[OK]   All referenced scripts exist

Would execute 5 steps in 3 tier(s) (max_parallel=2):
  tier 0: extract
  tier 1: transform, enrich
  tier 2: report
```

- Checks: YAML parse, secret resolution (`${env:}`, `${file:}`, `${vault:}`), R version constraint, renv detection and library readiness, freshness rules, referenced script existence, topo sort.
- Exit code 1 (and error printed) if any check is `[FAIL]`. Warnings do not fail.
- Cache status is intentionally out of scope — that's what `daggle plan` is for.
- No run directory is created; no events are written.

Implementation: `internal/cli/dryrun.go`, `--dry-run` flag wired into `runCmd`.

### Bounded parallel execution

DAG-level `max_parallel:` caps concurrent step execution via a semaphore. Prevents a single DAG with many independent steps from exhausting a laptop's resources.

```yaml
name: batch-reports
max_parallel: 2
steps:
  - id: report-a
    quarto: reports/a.qmd
  - id: report-b
    quarto: reports/b.qmd
  - id: report-c
    quarto: reports/c.qmd
  - id: report-d
    quarto: reports/d.qmd
```

- 0 (default) means unbounded — unchanged behavior.
- Negative values are rejected at DAG parse time.
- The semaphore is engine-scoped. Today tiers run sequentially, so in practice the cap applies per-tier; the design leaves room for cross-tier overlap without changing semantics.
- Orthogonal to step-level `max_parallel:` (which caps matrix expansions of a single step).

Implementation: `Engine.sem` in `internal/engine/engine.go`, acquired/released around each `runStep` in `runTier`. Field on `DAG` struct in `dag/dag.go`; validation in `dag/validate.go`.

## Summary of changes

| Area | Before | After |
|------|--------|-------|
| CLI commands | 23 | 25 (+`archive`, `verify`) |
| CLI flags on `run` | `--param` | +`--dry-run` |
| New DAG field | — | `max_parallel` |
| Run directory files | `events.jsonl`, `meta.json`, logs | +`dag.yaml`, optional `dag_diff.patch` |
| Packages | `internal/cli`, `internal/engine`, ... | +`internal/archive` |

## Files changed

- `dag/dag.go` — `DAG.MaxParallel` field
- `dag/validate.go` — reject negative `max_parallel`
- `internal/engine/engine.go` — semaphore threaded through `runTier`
- `internal/engine/engine_test.go` — `TestEngine_MaxParallel`
- `internal/archive/archive.go`, `internal/archive/verify.go`, `internal/archive/archive_test.go` — new package
- `internal/cli/archive.go`, `internal/cli/verify.go`, `internal/cli/archive_test.go`, `internal/cli/verify_test.go` — new commands
- `internal/cli/dryrun.go`, `internal/cli/dryrun_test.go` — dry-run reporter
- `internal/cli/run.go` — `--dry-run` flag, `dag.yaml` snapshot, diff trigger, `copyFile` helper
- `state/diff.go`, `state/diff_util.go`, `state/diff_test.go` — `WriteDAGDiff` and unified-diff helper
- `dag/dag_test.go` — `TestValidate_MaxParallel`
- `docs/design.md`, `docs/phase9.md`, `docs/daggle-schema.json` — design docs and JSON schema
- `site/reference/cli.qmd`, `site/reference/file-layout.qmd`, `site/writing-dags/yaml-reference.qmd`, `site/monitoring/archiving.qmd` — docs site

## What was deferred

Nothing from the Phase 9 scope. The four features in `docs/design.md`'s Phase 9 bullet list are all shipped here.
