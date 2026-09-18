package codeindex

import (
	"strings"

	"github.com/msuozzo/bonsai"
	bonsaiterraform "github.com/msuozzo/bonsai/bonsai-terraform"
)

type terraformExtractor struct{}

func init() {
	register("terraform", []string{".tf", ".tfvars"}, bonsaiterraform.NewParser, &terraformExtractor{})
}

func (e *terraformExtractor) Extract(root *bonsai.Node, src []byte) []Entry {
	var entries []Entry
	for _, child := range root.Children {
		if child.Kind == bonsaiterraform.KindBody {
			for _, bc := range child.Children {
				if bc.Kind == bonsaiterraform.KindBlock {
					entries = append(entries, e.extractBlock(bc, src))
				}
			}
		}
	}
	return entries
}

func (e *terraformExtractor) extractBlock(node *bonsai.Node, src []byte) Entry {
	var parts []string
	var attrs []string
	for _, c := range node.Children {
		switch c.Kind {
		case bonsaiterraform.KindIdentifier:
			parts = append(parts, string(c.Text(src)))
		case bonsaiterraform.KindStringLit:
			parts = append(parts, strings.Trim(string(c.Text(src)), `"`))
		case bonsaiterraform.KindBody:
			for _, bc := range c.Children {
				if bc.Kind == bonsaiterraform.KindAttribute {
					for _, ac := range bc.Children {
						if ac.Kind == bonsaiterraform.KindIdentifier {
							attrs = append(attrs, string(ac.Text(src)))
							break
						}
					}
				}
			}
		}
	}
	return Entry{
		Section:   SectionResource,
		Detail:    strings.Join(parts, " "),
		StartLine: int(node.StartPoint.Row) + 1,
		EndLine:   int(node.EndPoint.Row) + 1,
		Fields:    attrs,
	}
}
