package codeindex

import (
	"github.com/msuozzo/bonsai"
	bonsaiyaml "github.com/msuozzo/bonsai/bonsai-yaml"
)

type yamlExtractor struct{}

func init() {
	register("yaml", []string{".yaml", ".yml"}, bonsaiyaml.NewParser, &yamlExtractor{})
}

func (e *yamlExtractor) Extract(root *bonsai.Node, src []byte) []Entry {
	var entries []Entry
	for _, doc := range root.Children {
		if doc.Kind != bonsaiyaml.KindDocument {
			continue
		}
		for n := range doc.Find(bonsaiyaml.KindBlockMapping) {
			for _, pair := range n.Children {
				if pair.Kind != bonsaiyaml.KindBlockMappingPair {
					continue
				}
				key := textByField(pair, src, bonsaiyaml.FieldKey)
				entries = append(entries, Entry{
					Section:   SectionVar,
					Detail:    key,
					StartLine: int(pair.StartPoint.Row) + 1,
					EndLine:   int(pair.EndPoint.Row) + 1,
				})
			}
			break
		}
	}
	return entries
}
