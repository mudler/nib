package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
)

// nibMarkdownRenderer builds a glamour renderer styled to match nib's
// foreground-only, no-emoji aesthetic, word-wrapped at width columns.
func nibMarkdownRenderer(width int) (*glamour.TermRenderer, error) {
	if width < 1 {
		width = 1
	}
	return glamour.NewTermRenderer(
		glamour.WithStyles(nibMarkdownStyle()),
		glamour.WithWordWrap(width),
	)
}

// renderMarkdownWith renders content with r, trimming surrounding blank lines.
// If r is nil or rendering fails it falls back to plain render.Wrap so output is
// always shown.
func renderMarkdownWith(r *glamour.TermRenderer, content string, width int) string {
	if r == nil {
		return render.Wrap(content, width)
	}
	out, err := r.Render(content)
	if err != nil {
		return render.Wrap(content, width)
	}
	return strings.Trim(out, "\n")
}

// nibMarkdownStyle is a minimal glamour style: no background fills, no document
// margin (the TUI owns indentation), theme inks for headings/code/links.
// Code blocks get chroma syntax highlighting with theme-matched token colors.
func nibMarkdownStyle() ansi.StyleConfig {
	accent := string(theme.Accent)
	dim := string(theme.Dim)
	faint := string(theme.Faint)
	code := string(theme.Code)
	zero := uint(0)
	yes := true

	// Chroma expects hex color values, not 256-color codes. These are the
	// hex equivalents of the theme's 256-color palette.
	const (
		chrAccent = "#d7875f" // 173 — clay
		chrDim    = "#8a8a8a" // 245
		chrFaint  = "#585858" // 240
		chrCode   = "#af875f" // 137 — warm tan
		chrSage   = "#87af87" // 108 — muted green
		chrPurple = "#af87ff" // 141 — soft purple
	)

	p := func(s string) *string { return &s }

	return ansi.StyleConfig{
		Document: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{},
			Margin:         &zero,
		},
		Paragraph: ansi.StyleBlock{},
		BlockQuote: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color:  &dim,
				Italic: &yes,
				Prefix: theme.ApprovalGutter + " ",
			},
		},
		List: ansi.StyleList{
			StyleBlock:  ansi.StyleBlock{},
			LevelIndent: 2,
		},
		Heading: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Color: &accent, Bold: &yes},
		},
		Text:        ansi.StylePrimitive{},
		Strong:      ansi.StylePrimitive{Bold: &yes},
		Emph:        ansi.StylePrimitive{Italic: &yes},
		Item:        ansi.StylePrimitive{BlockPrefix: theme.Sep + " "},
		Enumeration: ansi.StylePrimitive{BlockPrefix: ". "},
		Link:        ansi.StylePrimitive{Color: &accent, Underline: &yes},
		LinkText:    ansi.StylePrimitive{Color: &accent},
		Code: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Color: &code},
		},
		CodeBlock: ansi.StyleCodeBlock{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{Color: &dim},
				Margin:         &zero,
			},
			Chroma: &ansi.Chroma{
				Text:     ansi.StylePrimitive{},
				Comment:  ansi.StylePrimitive{Color: p(chrFaint), Italic: &yes},
				Keyword:  ansi.StylePrimitive{Color: p(chrAccent), Bold: &yes},
				KeywordNamespace: ansi.StylePrimitive{Color: p(chrAccent)},
				KeywordType:      ansi.StylePrimitive{Color: p(chrCode)},
				Name:             ansi.StylePrimitive{},
				NameBuiltin:      ansi.StylePrimitive{Color: p(chrDim)},
				NameFunction:     ansi.StylePrimitive{Color: p(chrAccent)},
				NameClass:        ansi.StylePrimitive{Color: p(chrAccent), Bold: &yes},
				NameConstant:     ansi.StylePrimitive{Color: p(chrCode)},
				NameDecorator:    ansi.StylePrimitive{Color: p(chrAccent)},
				LiteralString:    ansi.StylePrimitive{Color: p(chrSage)},
				LiteralStringEscape: ansi.StylePrimitive{Color: p(chrDim)},
				LiteralNumber:    ansi.StylePrimitive{Color: p(chrPurple)},
				Operator:         ansi.StylePrimitive{Color: p(chrDim)},
				Punctuation:      ansi.StylePrimitive{Color: p(chrDim)},
			},
		},
		HorizontalRule: ansi.StylePrimitive{Color: &faint, Format: "\n──────\n"},
	}
}

// mdKey identifies one rendered markdown string in Model.mdCache.
type mdKey struct {
	width  int
	source string
}

// mdCacheLimit bounds Model.mdCache. When it is full the cache is cleared,
// which costs one full re-render, the same as having no cache.
const mdCacheLimit = 512

// renderMarkdown renders content at width through glamour and caches the
// result, so a redraw that changes nothing does not run glamour again.
func (m *Model) renderMarkdown(content string, width int) string {
	key := mdKey{width: width, source: content}
	if out, ok := m.mdCache[key]; ok {
		return out
	}
		out := renderMarkdownWith(m.markdownFor(width), stripMermaidFences(content), width)
	if m.mdCache == nil {
		m.mdCache = make(map[mdKey]string)
	}
	if len(m.mdCache) >= mdCacheLimit {
		clear(m.mdCache)
	}
	m.mdCache[key] = out
	return out
}

// mermaidFenceRe matches opening ```mermaid fences. It captures the
// remainder of the fence content up to the closing ``` so we can count
// nodes and replace the fence with a text placeholder.
var mermaidFenceRe = regexp.MustCompile("(?s)```mermaid\\b.*?```")

// stripMermaidFences replaces mermaid code fences with a text placeholder
// that reports the node count. The placeholder is a plain-text code block
// so glamour renders it as a code block without trying to parse it.
func stripMermaidFences(content string) string {
	if !strings.Contains(content, "```mermaid") {
		return content
	}
	return mermaidFenceRe.ReplaceAllStringFunc(content, func(fence string) string {
		// Count "nodes" by counting lines that look like a node
		// declaration. This is a heuristic, not a full parser — it
		// covers the most common forms (A[label], A-->B, A-->|text|B).
		lines := strings.Count(fence, "\n")
		nodes := countMermaidNodes(fence)
		if nodes == 0 {
			nodes = lines // fallback: approximate with line count
		}
		return "```text\n[Mermaid diagram — " + intToStr(nodes) + " nodes]\n```"
	})
}

// countMermaidNodes counts distinct node identifiers in a mermaid fence.
// A node identifier is an alphanumeric token at the start of an arrow
// declaration or in a [label] assignment.
var mermaidNodeRe = regexp.MustCompile(`(?m)^\s*([A-Za-z][A-Za-z0-9_]*)\s*(\[|\(|\{|\->|\-->|---|-\.|==)`)

func countMermaidNodes(fence string) int {
	seen := make(map[string]bool)
	for _, m := range mermaidNodeRe.FindAllStringSubmatch(fence, -1) {
		if len(m) > 1 {
			seen[m[1]] = true
		}
	}
	return len(seen)
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
