package dag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDatabaseDAG(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "db.yaml")
	yaml := "name: t\nsteps:\n  - id: q\n    database:\n" + body
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDatabase_ValidPostgres(t *testing.T) {
	body := "      driver: postgres\n" +
		"      query: SELECT 1\n" +
		"      output: out.csv\n"
	if _, err := ParseFile(writeDatabaseDAG(t, body)); err != nil {
		t.Fatalf("valid postgres DAG rejected: %v", err)
	}
}

func TestDatabase_InvalidDriver(t *testing.T) {
	body := "      driver: oracle\n" +
		"      query: SELECT 1\n" +
		"      output: out.csv\n"
	_, err := ParseFile(writeDatabaseDAG(t, body))
	if err == nil {
		t.Fatal("expected error for unknown driver, got nil")
	}
	if !strings.Contains(err.Error(), "database.driver") {
		t.Errorf("error should mention driver: %v", err)
	}
}

func TestDatabase_QueryAndFileBothSet(t *testing.T) {
	body := "      driver: sqlite\n" +
		"      query: SELECT 1\n" +
		"      query_file: q.sql\n" +
		"      output: out.rds\n"
	_, err := ParseFile(writeDatabaseDAG(t, body))
	if err == nil {
		t.Fatal("expected error for both query and query_file set, got nil")
	}
}

func TestDatabase_MissingQuery(t *testing.T) {
	body := "      driver: sqlite\n" +
		"      output: out.csv\n"
	_, err := ParseFile(writeDatabaseDAG(t, body))
	if err == nil {
		t.Fatal("expected error when no query source set, got nil")
	}
}

func TestDatabase_UnsupportedOutputExtension(t *testing.T) {
	body := "      driver: duckdb\n" +
		"      query: SELECT 1\n" +
		"      output: out.xml\n"
	_, err := ParseFile(writeDatabaseDAG(t, body))
	if err == nil {
		t.Fatal("expected error for unsupported extension, got nil")
	}
}

func TestDatabase_StepType(t *testing.T) {
	body := "      driver: duckdb\n" +
		"      query: SELECT 1\n" +
		"      output: out.parquet\n"
	d, err := ParseFile(writeDatabaseDAG(t, body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := StepType(d.Steps[0]); got != "database" {
		t.Errorf("StepType = %q, want database", got)
	}
}
