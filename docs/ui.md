# UI Ideas

daggle is headless-first — the built-in status UI stays minimal (DAG list, run detail, log viewer). The REST API and daggleR provide everything needed to build custom dashboards in Shiny or any other framework.

This document collects UI ideas for a future example Shiny app that demonstrates what's possible with the daggle API.

## Typed parameter forms

When triggering a DAG manually, render a form with typed inputs based on `params:` definitions. Support `type: choice` with dropdown options, booleans as checkboxes, and date pickers. Inspired by GitHub Actions' `workflow_dispatch` input types.

```yaml
params:
  - name: model_type
    type: choice
    options: [lm, glm, gam]
    default: lm
  - name: dry_run
    type: boolean
    default: false
  - name: start_date
    type: date
```

## Rich step summaries

Render `::daggle-summary::` metadata as formatted content: markdown tables, row counts as sparklines/time series across runs, key metrics with trend indicators. Show analysts the *result* of each step, not just pass/fail.

Possible views:
- Step detail page with rendered markdown summary
- Numeric metadata charted over time (e.g. row counts, model accuracy, coverage percentage)
- Plot thumbnails from artifact files (PNG/SVG outputs)

## Artifact browser

Browse output files per run with download links. Show file size, hash, and format. Enable cross-run comparison of artifacts (e.g. side-by-side plots from two runs).

## DAG visualization

Topological layout showing tiers and dependencies. Color-coded by step status (running, success, failed, skipped, cached). Gantt chart execution view showing step durations as horizontal bars.

## Freshness dashboard

Overview of all DAGs with source freshness status. Show which inputs are fresh, stale, or missing. Highlight DAGs that missed their deadline.

## Impact graph

Visualize exposure/impact tracking — which Shiny apps, dashboards, and reports depend on which DAGs. Answer "if this pipeline breaks, who is affected?"

## Run comparison view

Side-by-side diff of outputs, durations, and metadata between two runs of the same DAG. Highlight changes in step outputs and performance regressions.
