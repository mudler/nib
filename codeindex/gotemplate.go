package codeindex

import (
	"strings"

	"github.com/msuozzo/bonsai"
	bonsaigotemplate "github.com/msuozzo/bonsai/bonsai-gotemplate"
)

type gotemplateExtractor struct{}

func init() {
	register("gotemplate", []string{".tmpl", ".gotemplate", ".tpl"}, bonsaigotemplate.NewParser, &gotemplateExtractor{})
}

func (e *gotemplateExtractor) Extract(root *bonsai.Node, src []byte) []Entry {
	var entries []Entry
	for n := range root.Find(bonsaigotemplate.KindDefineAction) {
		name := e.actionName(n, src)
		entries = append(entries, Entry{
			Section:   SectionFunc,
			Name:      name,
			Detail:    name,
			StartLine: int(n.StartPoint.Row) + 1,
			EndLine:   int(n.EndPoint.Row) + 1,
		})
	}
	for n := range root.Find(bonsaigotemplate.KindBlockAction) {
		name := e.actionName(n, src)
		entries = append(entries, Entry{
			Section:   SectionFunc,
			Name:      name,
			Detail:    name,
			StartLine: int(n.StartPoint.Row) + 1,
			EndLine:   int(n.EndPoint.Row) + 1,
		})
	}
	for n := range root.Find(bonsaigotemplate.KindTemplateAction) {
		name := e.actionName(n, src)
		entries = append(entries, Entry{
			Section:   SectionImport,
			Detail:    name,
			StartLine: int(n.StartPoint.Row) + 1,
			EndLine:   int(n.EndPoint.Row) + 1,
		})
	}
	return entries
}

func (e *gotemplateExtractor) actionName(node *bonsai.Node, src []byte) string {
	if n := node.ChildByField(bonsaigotemplate.FieldName); n != nil {
		return strings.Trim(string(n.Text(src)), `"`)
	}
	for _, c := range node.Children {
		if c.Kind == bonsaigotemplate.KindInterpretedStringLiteral {
			return strings.Trim(string(c.Text(src)), `"`)
		}
	}
	return ""
}
