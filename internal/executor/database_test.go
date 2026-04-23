package executor

import (
	"strings"
	"testing"

	"github.com/cynkra/daggle/dag"
)

func TestGenerateDatabaseR_PostgresCSV(t *testing.T) {
	db := &dag.DatabaseStep{
		Driver: "postgres",
		Params: map[string]string{
			"dbname": "analytics",
			"host":   "localhost",
		},
		Query:  "SELECT * FROM orders",
		Output: "data/orders.csv",
	}
	code := generateDatabaseR(db)

	mustContain := []string{
		`RPostgres`, // driver package
		`RPostgres::Postgres()`,
		`dbname = "analytics"`,
		`host = "localhost"`,
		`"SELECT * FROM orders"`,
		`write.csv(df, "data/orders.csv", row.names = FALSE)`,
	}
	for _, s := range mustContain {
		if !strings.Contains(code, s) {
			t.Errorf("generated R code missing %q\ncode:\n%s", s, code)
		}
	}
}

func TestGenerateDatabaseR_DuckDBParquet(t *testing.T) {
	db := &dag.DatabaseStep{
		Driver: "duckdb",
		Query:  "SELECT 1",
		Output: "o.parquet",
	}
	code := generateDatabaseR(db)
	if !strings.Contains(code, "duckdb::duckdb()") {
		t.Errorf("expected duckdb driver expr, got:\n%s", code)
	}
	if !strings.Contains(code, "arrow::write_parquet(df, \"o.parquet\")") {
		t.Errorf("expected arrow::write_parquet call, got:\n%s", code)
	}
}

func TestGenerateDatabaseR_SQLiteRDS(t *testing.T) {
	db := &dag.DatabaseStep{
		Driver: "sqlite",
		Params: map[string]string{"dbname": "my.db"},
		Query:  "SELECT * FROM t",
		Output: "out.rds",
	}
	code := generateDatabaseR(db)
	if !strings.Contains(code, "RSQLite::SQLite()") {
		t.Errorf("expected sqlite driver, got:\n%s", code)
	}
	if !strings.Contains(code, `saveRDS(df, "out.rds")`) {
		t.Errorf("expected saveRDS call, got:\n%s", code)
	}
}

func TestGenerateDatabaseR_QueryFile(t *testing.T) {
	db := &dag.DatabaseStep{
		Driver:    "sqlite",
		Params:    map[string]string{"dbname": ":memory:"},
		QueryFile: "queries/orders.sql",
		Output:    "out.csv",
	}
	code := generateDatabaseR(db)
	if !strings.Contains(code, `readLines("queries/orders.sql")`) {
		t.Errorf("expected readLines(query_file), got:\n%s", code)
	}
}

func TestGenerateDatabaseR_ParamsStableOrder(t *testing.T) {
	// Two orderings of the same params should produce the same R code.
	a := &dag.DatabaseStep{
		Driver: "postgres",
		Params: map[string]string{"host": "h", "user": "u", "dbname": "d"},
		Query:  "SELECT 1",
		Output: "o.csv",
	}
	b := &dag.DatabaseStep{
		Driver: "postgres",
		Params: map[string]string{"user": "u", "dbname": "d", "host": "h"},
		Query:  "SELECT 1",
		Output: "o.csv",
	}
	if generateDatabaseR(a) != generateDatabaseR(b) {
		t.Error("expected stable R code regardless of map iteration order")
	}
}
