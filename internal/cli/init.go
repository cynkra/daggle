package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init <template>",
	Short: "Generate a DAG from a built-in template",
	Long:  "Available templates: pkg-check, pkg-release, data-pipeline",
	Args:  cobra.ExactArgs(1),
	RunE:  runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
}

func runInit(_ *cobra.Command, args []string) error {
	template := args[0]

	content, ok := templates[template]
	if !ok {
		return fmt.Errorf("unknown template %q; available: pkg-check, pkg-release, data-pipeline", template)
	}

	// Write to .daggle/ in current directory
	dagDir := ".daggle"
	if err := os.MkdirAll(dagDir, 0o755); err != nil {
		return fmt.Errorf("create .daggle directory: %w", err)
	}

	outPath := filepath.Join(dagDir, template+".yaml")
	if _, err := os.Stat(outPath); err == nil {
		return fmt.Errorf("file already exists: %s", outPath)
	}

	if err := os.WriteFile(outPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write template: %w", err)
	}

	fmt.Printf("Created %s\n", outPath)
	return nil
}

var templates = map[string]string{
	"pkg-check": `name: pkg-check
steps:
  - id: document
    document: "."

  - id: lint
    lint: "."
    depends: [document]

  - id: test
    test: "."
    depends: [document]

  - id: coverage
    coverage: "."
    depends: [test]

  - id: check
    check: "."
    depends: [document]

on_failure:
  command: echo "Package check failed"
`,

	"pkg-release": `name: pkg-release
params:
  - name: bump
    default: patch

steps:
  - id: document
    document: "."

  - id: lint
    lint: "."
    depends: [document]

  - id: test
    test: "."
    depends: [document]

  - id: check
    check: "."
    depends: [document]

  - id: review
    approve:
      message: "Review check results before releasing"
    depends: [lint, test, check]

  - id: pkgdown
    pkgdown: "."
    depends: [review]
`,

	"data-pipeline": `name: data-pipeline
params:
  - name: date
    default: "{{ .Today }}"

env:
  DATA_DIR: data
  OUTPUT_DIR: output

steps:
  - id: extract
    script: R/extract.R
    timeout: 30m
    retry:
      limit: 3

  - id: validate
    r_expr: |
      data <- readRDS(file.path(Sys.getenv("DATA_DIR"), "raw.rds"))
      stopifnot(nrow(data) > 0)
      cat("::daggle-output name=row_count::", nrow(data), "\n")
    depends: [extract]

  - id: transform
    script: R/transform.R
    depends: [validate]

  - id: report
    quarto: reports/summary.qmd
    depends: [transform]
`,
}
