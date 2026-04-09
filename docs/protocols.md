# Marker Protocols

daggle steps communicate structured data back to the engine by printing special marker lines to stdout. These markers are parsed by the engine, stripped from terminal output, and stored as structured data accessible via the API.

All markers follow the format `::daggle-<type> <params>::<value>`.

## Output Markers

Pass data between steps via environment variables.

```
::daggle-output name=<key>::<value>
```

- **name**: Identifier matching `[a-zA-Z_][a-zA-Z0-9_]*`.
- **value**: Arbitrary string (trimmed of leading/trailing whitespace).

Downstream steps receive outputs as environment variables named `DAGGLE_OUTPUT_<STEP_ID>_<KEY>` (uppercased, hyphens replaced with underscores).

### Examples

```
::daggle-output name=row_count::1542
::daggle-output name=file_path::/tmp/data.csv
```

### Behavior

- Multiple outputs per step are supported.
- If the same name is emitted twice, the last value wins.
- Outputs are collected after step completion and injected into the environment of all subsequent steps.
- Available via `GET /api/v1/dags/{name}/runs/{run_id}/outputs`.

## Summary Markers

Emit rich step summaries (typically markdown) for display in dashboards.

```
::daggle-summary format=<format>::<content>
```

- **format**: Content format identifier (e.g. `markdown`).
- **content**: The summary content.

### Examples

```
::daggle-summary format=markdown::## Results\n\nProcessed **42** rows successfully.
::daggle-summary format=markdown::Model accuracy: 0.95
```

### Behavior

- Multiple summaries per step are concatenated with newlines.
- Written to `{step_id}.summary.md` in the run directory.
- Available via `GET /api/v1/dags/{name}/runs/{run_id}/summaries`.

## Metadata Markers

Emit typed metadata entries for structured reporting.

```
::daggle-meta type=<type> name=<name>::<value>
```

- **type**: One of `numeric`, `text`, `table`, `image`.
- **name**: Identifier matching `[a-zA-Z_][a-zA-Z0-9_]*`.
- **value**: Value interpretation depends on type:
  - `numeric` -- A number as a string (e.g. `1542`, `0.95`).
  - `text` -- Free-form text.
  - `table` -- A JSON array of objects (e.g. `[{"x":1},{"x":2}]`).
  - `image` -- A file path relative to the working directory.

### Examples

```
::daggle-meta type=numeric name=row_count::1542
::daggle-meta type=text name=model_desc::Linear regression with 3 predictors
::daggle-meta type=table name=top5::[{"name":"Alice","score":98},{"name":"Bob","score":95}]
::daggle-meta type=image name=residuals::output/residuals.png
```

### Behavior

- Multiple metadata entries per step are supported.
- Written to `{step_id}.meta.json` in the run directory.
- Available via `GET /api/v1/dags/{name}/runs/{run_id}/metadata`.

## Validation Markers

Emit structured validation results for data quality checks.

```
::daggle-validation status=<status> name=<name>::<message>
```

- **status**: One of `pass`, `warn`, `fail`.
- **name**: Identifier matching `[a-zA-Z_][a-zA-Z0-9_]*`.
- **message**: Human-readable description of the validation result.

### Examples

```
::daggle-validation status=pass name=row_count::Expected > 0, got 1542
::daggle-validation status=warn name=missing_pct::12% missing (threshold: 20%)
::daggle-validation status=fail name=schema::Column 'date' expected date, got character
```

### Behavior

- Multiple validations per step are supported.
- Written to `{step_id}.validations.json` in the run directory.
- Available via `GET /api/v1/dags/{name}/runs/{run_id}/validations`.
- A validation with `status=fail` causes the step to be treated as failed when the step's `error_on` setting is `"error"` (the default). This applies even when the step process exits with code 0.
- Validations with `status=warn` are recorded but do not affect step success.
- Validations with `status=pass` are informational.

## General Notes

- All markers are stripped from terminal output but preserved in log files.
- Marker names must start with a letter or underscore, followed by letters, digits, or underscores.
- Markers are parsed line-by-line from stdout only (not stderr).
- Invalid marker lines (wrong format, unknown status, etc.) are treated as regular output.
