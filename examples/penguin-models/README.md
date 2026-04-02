# Penguin Model Comparison

Trains three classifiers on the Palmer Penguins dataset in parallel, then compares their accuracy. Demonstrates parallel execution and inter-step data passing.

## DAG structure

```
prepare → fit-lda ────┐
        → fit-tree ───┼→ compare
        → fit-knn ────┘
```

The three model fits run in parallel (same tier), and `compare` collects their accuracy scores via `::daggle-output::` markers.

## Requirements

- R with `palmerpenguins`, `MASS`, `rpart`, `class` (all base R except palmerpenguins, which is auto-installed)

## Run

```bash
cd examples/penguin-models
daggle run pipeline.yaml
```

## Output

- `data/train.rds` / `data/test.rds` — Train/test split (70/30)
- `models/lda_model.rds` — Fitted LDA model
- `models/tree_model.rds` — Fitted decision tree
- `models/comparison.csv` — Accuracy comparison table
