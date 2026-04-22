package state

import (
	"fmt"
	"strings"
)

// unifiedDiff returns a unified-diff representation of the line-level changes
// between a and b. Output mirrors `diff -u`:
//
//	--- labelA
//	+++ labelB
//	@@ -aStart,aLen +bStart,bLen @@
//	 context
//	-removed
//	+added
//
// Returns "" when a and b are identical.
//
// Implementation note: this emits one big hunk spanning the entire file,
// equivalent to `diff -U<infinity>`. For DAG YAML files (typically <500
// lines) the extra context is harmless, and the simpler implementation
// avoids an entire class of off-by-one bugs in hunk grouping.
func unifiedDiff(a, b []string, labelA, labelB string) string {
	ops := diffOps(a, b)
	if len(ops) == 0 {
		return ""
	}

	var lines []string
	aLen, bLen := 0, 0
	for _, op := range ops {
		switch op.kind {
		case ' ':
			lines = append(lines, " "+op.text)
			aLen++
			bLen++
		case '-':
			lines = append(lines, "-"+op.text)
			aLen++
		case '+':
			lines = append(lines, "+"+op.text)
			bLen++
		}
	}

	aStart := 1
	if aLen == 0 {
		aStart = 0
	}
	bStart := 1
	if bLen == 0 {
		bStart = 0
	}

	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n", labelA)
	fmt.Fprintf(&out, "+++ %s\n", labelB)
	fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", aStart, aLen, bStart, bLen)
	for _, line := range lines {
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}

type diffOp struct {
	kind byte // ' ' equal, '-' deletion, '+' insertion
	text string
}

// diffOps returns the LCS-derived edit script for converting a into b.
func diffOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	var ops []diffOp
	i, j := n, m
	for i > 0 && j > 0 {
		if a[i-1] == b[j-1] {
			ops = append(ops, diffOp{kind: ' ', text: a[i-1]})
			i--
			j--
		} else if dp[i-1][j] >= dp[i][j-1] {
			ops = append(ops, diffOp{kind: '-', text: a[i-1]})
			i--
		} else {
			ops = append(ops, diffOp{kind: '+', text: b[j-1]})
			j--
		}
	}
	for i > 0 {
		ops = append(ops, diffOp{kind: '-', text: a[i-1]})
		i--
	}
	for j > 0 {
		ops = append(ops, diffOp{kind: '+', text: b[j-1]})
		j--
	}

	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}

	for _, op := range ops {
		if op.kind != ' ' {
			return ops
		}
	}
	return nil
}

// splitLines splits s into lines, dropping a single trailing newline so we
// do not introduce a phantom empty line.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	trimmed := strings.TrimRight(s, "\n")
	if trimmed == "" {
		return []string{""}
	}
	return strings.Split(trimmed, "\n")
}
