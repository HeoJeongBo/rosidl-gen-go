package generate

import (
	"fmt"
	"strings"
)

// maxDiffLines caps the quadratic LCS below. Generated files are far smaller
// than this; the cap only stops a pathological input from hanging the tool.
const maxDiffLines = 5000

// diffLines renders a unified-style diff of want against got. Under -check the
// interesting question is never "did it change" — the exit code already says
// that — but "what changed": a reordered field is a wire-breaking change, a
// reflowed comment is not, and the two are indistinguishable from a file name.
func diffLines(got, want []byte) string {
	a := splitLines(got)
	b := splitLines(want)

	if len(a) > maxDiffLines || len(b) > maxDiffLines {
		return fmt.Sprintf("\t(%d lines on disk, %d generated; too large to diff)\n", len(a), len(b))
	}

	var out strings.Builder
	for _, h := range hunks(a, b) {
		out.WriteString(h)
	}
	if out.Len() == 0 {
		// Equal line-wise but not byte-wise: a trailing-newline difference.
		return "\t(files differ only in trailing whitespace)\n"
	}
	return out.String()
}

func splitLines(b []byte) []string {
	s := strings.TrimSuffix(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// hunks walks the LCS backtrace and emits one block per run of changes, with
// up to `context` unchanged lines on each side.
func hunks(a, b []string) []string {
	const context = 2

	ops := lcsOps(a, b)

	var out []string
	for i := 0; i < len(ops); {
		if ops[i].kind == opEqual {
			i++
			continue
		}
		// Extend to the end of this run of changes.
		j := i
		for j < len(ops) && ops[j].kind != opEqual {
			j++
		}
		lo := max(i-context, 0)
		hi := min(j+context, len(ops))

		var h strings.Builder
		for _, op := range ops[lo:hi] {
			switch op.kind {
			case opEqual:
				fmt.Fprintf(&h, "\t  %s\n", op.text)
			case opDelete:
				fmt.Fprintf(&h, "\t- %s\n", op.text)
			case opInsert:
				fmt.Fprintf(&h, "\t+ %s\n", op.text)
			}
		}
		out = append(out, h.String())
		i = j
	}
	return out
}

type opKind int

const (
	opEqual opKind = iota
	opDelete
	opInsert
)

type op struct {
	kind opKind
	text string
}

// lcsOps is a textbook longest-common-subsequence diff. `-` is a line present
// on disk, `+` a line this run would write.
func lcsOps(a, b []string) []op {
	n, m := len(a), len(b)
	table := make([][]int, n+1)
	for i := range table {
		table[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else {
				table[i][j] = max(table[i+1][j], table[i][j+1])
			}
		}
	}

	var out []op
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, op{opEqual, a[i]})
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			out = append(out, op{opDelete, a[i]})
			i++
		default:
			out = append(out, op{opInsert, b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, op{opDelete, a[i]})
	}
	for ; j < m; j++ {
		out = append(out, op{opInsert, b[j]})
	}
	return out
}
