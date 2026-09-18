package codeindex

import (
	"strings"

	"github.com/msuozzo/bonsai"
	bonsaikotlin "github.com/msuozzo/bonsai/bonsai-kotlin"
)

type kotlinExtractor struct{}

func init() {
	register("kotlin", []string{".kt", ".kts"}, bonsaikotlin.NewParser, &kotlinExtractor{})
}

func (e *kotlinExtractor) Extract(root *bonsai.Node, src []byte) []Entry {
	var entries []Entry
	for _, child := range root.Children {
		switch child.Kind {
		case bonsaikotlin.KindPackageHeader:
			name := strings.TrimPrefix(compactText(child, src), "package ")
			entries = append(entries, Entry{
				Section:   SectionPackage,
				Name:      name,
				Detail:    name,
				StartLine: int(child.StartPoint.Row) + 1,
				EndLine:   int(child.EndPoint.Row) + 1,
			})
		case bonsaikotlin.KindImport:
			entries = append(entries, Entry{
				Section:   SectionImport,
				Detail:    compactText(child, src),
				StartLine: int(child.StartPoint.Row) + 1,
				EndLine:   int(child.EndPoint.Row) + 1,
			})
		case bonsaikotlin.KindClassDeclaration:
			entries = append(entries, e.extractClass(child, src))
		case bonsaikotlin.KindObjectDeclaration:
			entries = append(entries, e.extractObject(child, src))
		case bonsaikotlin.KindFunctionDeclaration:
			entries = append(entries, e.extractFunction(child, src))
		case bonsaikotlin.KindPropertyDeclaration:
			entries = append(entries, e.extractProperty(child, src))
		case bonsaikotlin.KindTypeAlias:
			entries = append(entries, e.extractTypeAlias(child, src))
		}
	}
	return entries
}

func (e *kotlinExtractor) extractClass(node *bonsai.Node, src []byte) Entry {
	name := textByField(node, src, bonsaikotlin.FieldName)
	return Entry{
		Section:   SectionClass,
		Name:      name,
		Detail:    name,
		StartLine: int(node.StartPoint.Row) + 1,
		EndLine:   int(node.EndPoint.Row) + 1,
		Fields:    e.classMembers(node, src),
	}
}

func (e *kotlinExtractor) extractObject(node *bonsai.Node, src []byte) Entry {
	name := textByField(node, src, bonsaikotlin.FieldName)
	return Entry{
		Section:   SectionClass,
		Name:      name,
		Detail:    name,
		StartLine: int(node.StartPoint.Row) + 1,
		EndLine:   int(node.EndPoint.Row) + 1,
		Fields:    e.classMembers(node, src),
	}
}

func (e *kotlinExtractor) classMembers(node *bonsai.Node, src []byte) []string {
	var members []string
	for _, c := range node.Children {
		if c.Kind != bonsaikotlin.KindClassBody {
			continue
		}
		for _, bc := range c.Children {
			switch bc.Kind {
			case bonsaikotlin.KindFunctionDeclaration:
				members = append(members, e.functionSig(bc, src))
			case bonsaikotlin.KindPropertyDeclaration:
				members = append(members, e.propertySig(bc, src))
			}
		}
	}
	return members
}

func (e *kotlinExtractor) extractFunction(node *bonsai.Node, src []byte) Entry {
	return Entry{
		Section:   SectionFunc,
		Name:      textByField(node, src, bonsaikotlin.FieldName),
		Detail:    e.functionSig(node, src),
		StartLine: int(node.StartPoint.Row) + 1,
		EndLine:   int(node.EndPoint.Row) + 1,
	}
}

func (e *kotlinExtractor) extractProperty(node *bonsai.Node, src []byte) Entry {
	name := e.propertyName(node, src)
	return Entry{
		Section:   SectionVar,
		Name:      name,
		Detail:    e.propertySig(node, src),
		StartLine: int(node.StartPoint.Row) + 1,
		EndLine:   int(node.EndPoint.Row) + 1,
	}
}

func (e *kotlinExtractor) extractTypeAlias(node *bonsai.Node, src []byte) Entry {
	name := e.typeName(node, src)
	typ := e.returnType(node, src)
	detail := name
	if typ != "" {
		detail = name + " = " + typ
	}
	return Entry{
		Section:   SectionType,
		Name:      name,
		Detail:    detail,
		StartLine: int(node.StartPoint.Row) + 1,
		EndLine:   int(node.EndPoint.Row) + 1,
	}
}

func (e *kotlinExtractor) functionSig(node *bonsai.Node, src []byte) string {
	name := textByField(node, src, bonsaikotlin.FieldName)
	var params []string
	for pn := range node.Find(bonsaikotlin.KindParameter) {
		for _, c := range pn.Children {
			if c.Kind == bonsaikotlin.KindIdentifier {
				params = append(params, string(c.Text(src)))
				break
			}
		}
	}
	sig := name + "(" + strings.Join(params, ", ") + ")"
	if retType := e.returnType(node, src); retType != "" {
		sig = retType + " " + sig
	}
	return sig
}

func (e *kotlinExtractor) propertySig(node *bonsai.Node, src []byte) string {
	name := e.propertyName(node, src)
	if typ := e.propertyType(node, src); typ != "" {
		return name + ": " + typ
	}
	return name
}

func (e *kotlinExtractor) returnType(node *bonsai.Node, src []byte) string {
	for _, c := range node.Children {
		switch c.Kind {
		case bonsaikotlin.KindUserType,
			bonsaikotlin.KindFunctionType,
			bonsaikotlin.KindNullableType,
			bonsaikotlin.KindNonNullableType:
			return compactText(c, src)
		}
	}
	return ""
}

func (e *kotlinExtractor) propertyType(node *bonsai.Node, src []byte) string {
	for _, c := range node.Children {
		if c.Kind == bonsaikotlin.KindVariableDeclaration {
			for _, vc := range c.Children {
				switch vc.Kind {
				case bonsaikotlin.KindUserType,
					bonsaikotlin.KindNullableType,
					bonsaikotlin.KindNonNullableType,
					bonsaikotlin.KindFunctionType:
					return compactText(vc, src)
				}
			}
		}
	}
	return ""
}

func (e *kotlinExtractor) propertyName(node *bonsai.Node, src []byte) string {
	for _, c := range node.Children {
		if c.Kind == bonsaikotlin.KindVariableDeclaration {
			for _, vc := range c.Children {
				if vc.Kind == bonsaikotlin.KindIdentifier {
					return string(vc.Text(src))
				}
			}
		}
	}
	return ""
}

func (e *kotlinExtractor) typeName(node *bonsai.Node, src []byte) string {
	for _, c := range node.Children {
		if c.Kind == bonsaikotlin.KindIdentifier {
			return string(c.Text(src))
		}
	}
	return ""
}
