package codeindex

import (
	"github.com/msuozzo/bonsai"
	bonsaidockerfile "github.com/msuozzo/bonsai/bonsai-dockerfile"
)

type dockerfileExtractor struct{}

func init() {
	register("dockerfile", []string{".dockerfile", "dockerfile"}, bonsaidockerfile.NewParser, &dockerfileExtractor{})
}

func (e *dockerfileExtractor) Extract(root *bonsai.Node, src []byte) []Entry {
	var entries []Entry
	for _, child := range root.Children {
		switch child.Kind {
		case bonsaidockerfile.KindFromInstruction:
			entries = append(entries, e.instr(child, src, SectionImport))
		case bonsaidockerfile.KindArgInstruction, bonsaidockerfile.KindEnvInstruction:
			entries = append(entries, e.instr(child, src, SectionVar))
		case bonsaidockerfile.KindLabelInstruction:
			entries = append(entries, e.instr(child, src, SectionConst))
		case bonsaidockerfile.KindRunInstruction,
			bonsaidockerfile.KindCmdInstruction,
			bonsaidockerfile.KindEntrypointInstruction,
			bonsaidockerfile.KindCopyInstruction,
			bonsaidockerfile.KindAddInstruction,
			bonsaidockerfile.KindExposeInstruction,
			bonsaidockerfile.KindVolumeInstruction,
			bonsaidockerfile.KindUserInstruction,
			bonsaidockerfile.KindWorkdirInstruction,
			bonsaidockerfile.KindOnbuildInstruction,
			bonsaidockerfile.KindStopsignalInstruction,
			bonsaidockerfile.KindHealthcheckInstruction,
			bonsaidockerfile.KindShellInstruction,
			bonsaidockerfile.KindMaintainerInstruction,
			bonsaidockerfile.KindCrossBuildInstruction:
			entries = append(entries, e.instr(child, src, SectionFunc))
		}
	}
	return entries
}

func (e *dockerfileExtractor) instr(node *bonsai.Node, src []byte, section Section) Entry {
	return Entry{
		Section:   section,
		Detail:    compactText(node, src),
		StartLine: int(node.StartPoint.Row) + 1,
		EndLine:   int(node.EndPoint.Row) + 1,
	}
}
