package executor

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cynkra/daggle/dag"
)

// DatabaseExecutor runs a SQL query via R DBI and writes the result to disk.
type DatabaseExecutor struct{}

// Run generates R code that connects to the database, runs the query, and
// writes the result set in the format inferred from the output extension.
func (e *DatabaseExecutor) Run(ctx context.Context, step dag.Step, logDir string, workdir string, env []string) Result {
	rCode := wrapErrorOn(generateDatabaseR(step.Database), step.ErrorOn)
	s := step
	s.Args = nil
	return runRScript(ctx, rCode, s, logDir, workdir, env, "database")
}

// driverRSpec maps canonical driver names to the R package and DBI driver
// function used to construct a DBIDriver for dbConnect.
var driverRSpec = map[string]struct {
	pkg  string
	expr string
}{
	"postgres": {pkg: "RPostgres", expr: "RPostgres::Postgres()"},
	"mysql":    {pkg: "RMariaDB", expr: "RMariaDB::MariaDB()"},
	"mariadb":  {pkg: "RMariaDB", expr: "RMariaDB::MariaDB()"},
	"sqlite":   {pkg: "RSQLite", expr: "RSQLite::SQLite()"},
	"duckdb":   {pkg: "duckdb", expr: "duckdb::duckdb()"},
	"odbc":     {pkg: "odbc", expr: "odbc::odbc()"},
}

func generateDatabaseR(db *dag.DatabaseStep) string {
	spec, ok := driverRSpec[db.Driver]
	if !ok {
		return fmt.Sprintf("stop('unknown database driver: %s')\n", db.Driver)
	}

	// Params are passed as named dbConnect args. Sort keys so the generated
	// R code is stable across runs (easier to debug and cache-key).
	keys := make([]string, 0, len(db.Params))
	for k := range db.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var paramsArgs strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&paramsArgs, ", %s = %q", k, db.Params[k])
	}

	// Query source: either inline or read from a file.
	var queryExpr string
	switch {
	case db.Query != "":
		queryExpr = fmt.Sprintf("%q", db.Query)
	case db.QueryFile != "":
		queryExpr = fmt.Sprintf("paste(readLines(%q), collapse = \"\\n\")", db.QueryFile)
	default:
		queryExpr = `""`
	}

	writeExpr := writeOutputR(db.Output)

	return fmt.Sprintf(`if (!requireNamespace("DBI", quietly = TRUE)) stop("database step requires the DBI package. Install with: install.packages('DBI')")
if (!requireNamespace(%q, quietly = TRUE)) stop("database step requires the %s package. Install with: install.packages(%q)")
cat("Connecting to database (%s)...\n")
con <- DBI::dbConnect(%s%s)
on.exit(DBI::dbDisconnect(con), add = TRUE)
query <- %s
cat("Running query...\n")
df <- DBI::dbGetQuery(con, query)
cat(sprintf("Fetched %%d row(s), %%d column(s)\n", nrow(df), ncol(df)))
cat(sprintf("::daggle-output name=row_count::%%d\n", nrow(df)))
dir.create(dirname(%q), recursive = TRUE, showWarnings = FALSE)
%s
cat(sprintf("Wrote %%s\n", %q))
`,
		spec.pkg, spec.pkg, spec.pkg,
		db.Driver,
		spec.expr, paramsArgs.String(),
		queryExpr,
		db.Output,
		writeExpr,
		db.Output,
	)
}

// writeOutputR returns an R expression (without trailing newline) that writes
// the data.frame `df` to the given output path, with format inferred from the
// file extension.
func writeOutputR(output string) string {
	ext := strings.ToLower(filepath.Ext(output))
	switch ext {
	case ".csv":
		return fmt.Sprintf("write.csv(df, %q, row.names = FALSE)", output)
	case ".tsv":
		return fmt.Sprintf("write.table(df, %q, sep = \"\\t\", row.names = FALSE, quote = FALSE)", output)
	case ".rds":
		return fmt.Sprintf("saveRDS(df, %q)", output)
	case ".parquet":
		return fmt.Sprintf(`if (!requireNamespace("arrow", quietly = TRUE)) stop("parquet output requires the arrow package. Install with: install.packages('arrow')")
arrow::write_parquet(df, %q)`, output)
	case ".feather":
		return fmt.Sprintf(`if (!requireNamespace("arrow", quietly = TRUE)) stop("feather output requires the arrow package. Install with: install.packages('arrow')")
arrow::write_feather(df, %q)`, output)
	default:
		return fmt.Sprintf("stop('unsupported output extension: %s')", ext)
	}
}
