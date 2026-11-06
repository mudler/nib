package watch

import (
	"fmt"
	"strings"
)

const (
	diffContext = 1
	// lcsLimit bounds the table of the line diff. A larger change is shown as
	// one block: the old lines removed, the new lines added.
	lcsLimit = 1 << 22
)

type op struct {
	kind byte // ' ', '-' or '+'
	text string
}

// Diff renders the lines that changed from old to new as a unified diff with
// one line of context. It returns "" when the two are equal. The result is cut
// at max bytes, on a line boundary, and ends with a truncation marker when it
// was cut. A max of zero or less means no cut.
func Diff(old, new string, max int) string {
	if old == new {
		return ""
	}
	a, b := splitLines(old), splitLines(new)
	ops := edits(a, b)

	var sb strings.Builder
	sb.WriteString("--- previous\n+++ current\n")
	for _, h := range hunks(ops) {
		sb.WriteString(h)
	}
	out := sb.String()
	if max > 0 && len(out) > max {
		out = Cap(out, max)
		if i := strings.LastIndexByte(out, '\n'); i >= 0 {
			out = out[:i+1]
		}
		out += "... (truncated)\n"
	}
	return out
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// edits returns the full edit script from a to b.
func edits(a, b []string) []op {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]

	var ops []op
	for _, l := range a[:pre] {
		ops = append(ops, op{' ', l})
	}
	ops = append(ops, middle(ma, mb)...)
	for _, l := range a[len(a)-suf:] {
		ops = append(ops, op{' ', l})
	}
	return ops
}

func middle(a, b []string) []op {
	var ops []op
	if len(a)*len(b) > lcsLimit || len(a) == 0 || len(b) == 0 {
		for _, l := range a {
			ops = append(ops, op{'-', l})
		}
		for _, l := range b {
			ops = append(ops, op{'+', l})
		}
		return ops
	}
	// lcs[i][j] is the length of the longest common subsequence of a[i:] and b[j:].
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{' ', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, op{'-', a[i]})
			i++
		default:
			ops = append(ops, op{'+', b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		ops = append(ops, op{'-', a[i]})
	}
	for ; j < len(b); j++ {
		ops = append(ops, op{'+', b[j]})
	}
	return ops
}

// hunks groups the changes of ops, each with diffContext lines around it.
func hunks(ops []op) []string {
	var out []string
	n := len(ops)
	for i := 0; i < n; {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start := max(i-diffContext, 0)
		end := i
		// Extend while another change starts within the context window.
		for end < n {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			next := end
			for next < n && ops[next].kind == ' ' {
				next++
			}
			if next < n && next-end <= 2*diffContext {
				end = next
				continue
			}
			break
		}
		stop := min(end+diffContext, n)
		oldStart, newStart := 1, 1
		for _, o := range ops[:start] {
			if o.kind != '+' {
				oldStart++
			}
			if o.kind != '-' {
				newStart++
			}
		}
		var oldN, newN int
		var body strings.Builder
		for _, o := range ops[start:stop] {
			if o.kind != '+' {
				oldN++
			}
			if o.kind != '-' {
				newN++
			}
			body.WriteByte(o.kind)
			body.WriteString(o.text)
			body.WriteByte('\n')
		}
		out = append(out, fmt.Sprintf("@@ -%d,%d +%d,%d @@\n%s", oldStart, oldN, newStart, newN, body.String()))
		i = stop
	}
	return out
}
