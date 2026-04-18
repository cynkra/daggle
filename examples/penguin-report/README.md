# Penguin Report Pipeline

A data wrangling pipeline that cleans the Palmer Penguins dataset, computes summary statistics, generates plots, and renders a Quarto report.

## DAG structure

```
setup → extract → clean → summarize ─┐
                       └→ plot ───────┴→ report
```

`summarize` and `plot` run in parallel (both depend on `clean`), and `report` waits for both.

## Requirements

- R with `palmerpenguins` (auto-installed if missing)
- [Quarto CLI](https://quarto.org/docs/get-started/)

## Run

```bash
cd examples/penguin-report
daggle run pipeline.yaml
```

## Output

- `data/raw_penguins.rds` — Raw dataset
- `data/clean_penguins.rds` — Cleaned dataset (missing values removed)
- `output/species_summary.csv` — Per-species summary statistics
- `output/bill_scatter.png` — Bill length vs depth scatter plot
- `output/flipper_boxplot.png` — Flipper length boxplot
- `output/report.html` — Final Quarto report

## Exposures

The pipeline declares its downstream consumers via `exposures:` so they show up in `daggle impact penguin-report`. Use this when you need to warn stakeholders before changing the report's columns or dropping a step.
