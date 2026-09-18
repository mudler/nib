package codeindex

import (
	"strings"

	"github.com/msuozzo/bonsai"
	bonsaimarkdown "github.com/msuozzo/bonsai/bonsai-markdown"
)

type markdownExtractor struct{}

func init() {
	register("markdown", []string{".md", ".markdown"}, bonsaimarkdown.NewParser, &markdownExtractor{})
}

func (e *markdownExtractor) Extract(root *bonsai.Node, src []byte) []Entry {
	var entries []Entry
	for n := range root.Find(bonsaimarkdown.KindAtxHeading) {
		entries = append(entries, e.heading(n, src))
	}
	for n := range root.Find(bonsaimarkdown.KindSetextHeading) {
		entries = append(entries, e.heading(n, src))
	}
	return entries
}

func (e *markdownExtractor) heading(node *bonsai.Node, src []byte) Entry {
	content := textByField(node, src, bonsaimarkdown.FieldHeadingContent)
	if content == "" {
		content = strings.TrimSpace(strings.TrimLeft(compactText(node, src), "#"))
	}
	return Entry{
		Section:   SectionHeading,
		Detail:    content,
		StartLine: int(node.StartPoint.Row) + 1,
		EndLine:   int(node.EndPoint.Row) + 1,
	}
}
