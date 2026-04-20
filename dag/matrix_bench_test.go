package dag

import (
	"fmt"
	"testing"
)

// BenchmarkExpandMatrix exercises a 5x5x5 matrix with 10 args. This is
// a stress test — real-world matrices are usually smaller, but this is
// what catches quadratic scaling in the interpolation loop.
func BenchmarkExpandMatrix(b *testing.B) {
	args := make([]string, 10)
	for i := range args {
		args[i] = fmt.Sprintf("--flag-%d={{ .Matrix.a }}-{{ .Matrix.b }}-{{.Matrix.c}}", i)
	}
	tpl := Step{
		ID: "run",
		Matrix: map[string][]string{
			"a": {"1", "2", "3", "4", "5"},
			"b": {"x", "y", "z", "w", "v"},
			"c": {"p", "q", "r", "s", "t"},
		},
		Args:       args,
		OutputDir:  "out/{{ .Matrix.a }}/{{ .Matrix.b }}",
		OutputName: "{{.Matrix.c}}.csv",
	}
	steps := []Step{tpl}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ExpandMatrix(steps)
	}
}
