package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/theme"
)

// renderAsk renders the agent's question (and numbered options, if any). The
// glyph hints the selection mode: ( ) radio for single-select, [ ] checkbox for
// multi-select. The block is set off by a left gutter rule rather than a box,
// matching the tool-approval idiom.
func renderAsk(req chat.AskRequest, width int) string {
	gutter := theme.Gutter.Render(theme.ApprovalGutter) + " "
	var b strings.Builder
	b.WriteString(gutter + theme.LabelNib.Render(req.Question))
	b.WriteString("\n")
	marker := "( )"
	if req.MultiSelect {
		marker = "[ ]"
	}
	for i, o := range req.Options {
		fmt.Fprintf(&b, "%s%s %s %s\n", gutter, theme.Prompt.Render(marker), theme.ApproveKey.Render(fmt.Sprintf("%d.", i+1)), theme.Help.Render(o))
	}
	switch {
	case len(req.Options) > 0 && req.MultiSelect:
		b.WriteString(gutter + theme.Hint.Render("type numbers separated by commas (e.g. 1,3), or type your own answer."))
	case len(req.Options) > 0:
		b.WriteString(gutter + theme.Hint.Render("type a number to pick, or type your own answer."))
	default:
		b.WriteString(gutter + theme.Hint.Render("type your answer."))
	}
	return strings.TrimRight(b.String(), "\n")
}

// parseAskAnswer maps a typed answer onto req.Options. For single-select a lone
// 1-based index returns that option. For multi-select a list of indices (comma-
// or space-separated, e.g. "1,3" or "1 3") returns the chosen options joined by
// ", ". Anything that doesn't parse cleanly as indices is returned verbatim as a
// free-form answer.
func parseAskAnswer(input string, req chat.AskRequest) string {
	trimmed := strings.TrimSpace(input)
	if len(req.Options) == 0 {
		return input
	}

	if req.MultiSelect {
		fields := strings.FieldsFunc(trimmed, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t'
		})
		var chosen []string
		for _, f := range fields {
			n, err := strconv.Atoi(f)
			if err != nil || n < 1 || n > len(req.Options) {
				return input // not a clean index list → treat as free text
			}
			chosen = append(chosen, req.Options[n-1])
		}
		if len(chosen) > 0 {
			return strings.Join(chosen, ", ")
		}
		return input
	}

	if n, err := strconv.Atoi(trimmed); err == nil && n >= 1 && n <= len(req.Options) {
		return req.Options[n-1]
	}
	return input
}
