package codeindex

import (
	"strings"

	"github.com/msuozzo/bonsai"
	bonsaigroovy "github.com/msuozzo/bonsai/bonsai-groovy"
)

type groovyExtractor struct{}

func init() {
	register("groovy", []string{".groovy", ".gradle", "jenkinsfile"}, bonsaigroovy.NewParser, &groovyExtractor{})
}

func (e *groovyExtractor) Extract(root *bonsai.Node, src []byte) []Entry {
	var entries []Entry
	for _, child := range root.Children {
		switch child.Kind {
		case bonsaigroovy.KindGroovyPackage:
			name := strings.TrimPrefix(compactText(child, src), "package ")
			entries = append(entries, Entry{
				Section:   SectionPackage,
				Name:      name,
				Detail:    name,
				StartLine: int(child.StartPoint.Row) + 1,
				EndLine:   int(child.EndPoint.Row) + 1,
			})
		case bonsaigroovy.KindGroovyImport:
			entries = append(entries, Entry{
				Section:   SectionImport,
				Detail:    compactText(child, src),
				StartLine: int(child.StartPoint.Row) + 1,
				EndLine:   int(child.EndPoint.Row) + 1,
			})
		case bonsaigroovy.KindClassDefinition:
			entries = append(entries, e.extractClass(child, src))
		case bonsaigroovy.KindFunctionDefinition:
			entries = append(entries, e.extractFunction(child, src))
		}
	}
	return entries
}

func (e *groovyExtractor) extractClass(node *bonsai.Node, src []byte) Entry {
	name := textByField(node, src, bonsaigroovy.FieldName)
	detail := name
	if sc := node.ChildByField(bonsaigroovy.FieldSuperclass); sc != nil {
		detail += " extends " + compactText(sc, src)
	}
	var members []string
	for fn := range node.Find(bonsaigroovy.KindFunctionDefinition) {
		members = append(members, e.functionSig(fn, src))
	}
	return Entry{
		Section:   SectionClass,
		Name:      name,
		Detail:    detail,
		StartLine: int(node.StartPoint.Row) + 1,
		EndLine:   int(node.EndPoint.Row) + 1,
		Fields:    members,
	}
}

func (e *groovyExtractor) returnType(node *bonsai.Node, src []byte) string {
	rt := node.ChildByField(bonsaigroovy.FieldType)
	if rt == nil || !rt.Named {
		return ""
	}
	return string(rt.Text(src))
}

func (e *groovyExtractor) extractFunction(node *bonsai.Node, src []byte) Entry {
	name := textByField(node, src, bonsaigroovy.FieldFunction)
	params := textByField(node, src, bonsaigroovy.FieldParameters)
	retType := e.returnType(node, src)
	sig := name + params
	if retType != "" {
		sig = retType + " " + sig
	}
	return Entry{
		Section:   SectionFunc,
		Name:      name,
		Detail:    sig,
		StartLine: int(node.StartPoint.Row) + 1,
		EndLine:   int(node.EndPoint.Row) + 1,
	}
}

func (e *groovyExtractor) functionSig(node *bonsai.Node, src []byte) string {
	name := textByField(node, src, bonsaigroovy.FieldFunction)
	params := textByField(node, src, bonsaigroovy.FieldParameters)
	retType := e.returnType(node, src)
	sig := name + params
	if retType != "" {
		sig = retType + " " + sig
	}
	return sig
}
