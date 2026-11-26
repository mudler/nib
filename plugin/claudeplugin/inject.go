package claudeplugin

import (
	"regexp"
	"strings"
)

// InjectionKind is the kind of a dynamic reference in a command or skill body.
type InjectionKind string

const (
	// InjectShell is an inline !`cmd`: the command's output replaces it.
	InjectShell InjectionKind = "shell"
	// InjectShellBlock is a fenced block opened with ```!: each line is a
	// command and the output replaces the block.
	InjectShellBlock InjectionKind = "shell-block"
	// InjectFile is an @path reference: the file's content is attached.
	InjectFile InjectionKind = "file"
)

// Injection is a dynamic reference found in a body. The package never
// resolves it: a host that supports it runs Text (for shell kinds) or reads
// Text (for files) with its own executor, then replaces Raw in the body.
type Injection struct {
	Kind InjectionKind
	// Text is the command (shell kinds) or the path (file).
	Text string
	// Raw is the exact text in the body that the result replaces.
	Raw string
}

var (
	inlineShell = regexp.MustCompile("(?m)(^|[ \t(])(!`([^`\n]+)`)")
	fileRef     = regexp.MustCompile(`(^|[\s(])(@([^\s` + "`" + `'"<>()\[\]{}]+))`)
)

// ParseInjections returns the shell injections and @file references in body,
// in order of appearance. An inline !`cmd` counts only at the start of a line
// or after whitespace, as in Claude Code. An @file reference must hold a '/'
// or a '.', and is not read inside code fences or inline code, so decorators
// and package names in code samples are not reported.
func ParseInjections(body string) []Injection {
	var out []Injection
	lines := strings.SplitAfter(body, "\n")
	inFence := false
	var block strings.Builder
	var blockRaw strings.Builder
	inShellBlock := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if inShellBlock {
			blockRaw.WriteString(line)
			if strings.HasPrefix(trim, "```") {
				out = append(out, Injection{Kind: InjectShellBlock, Text: strings.TrimRight(block.String(), "\n"), Raw: strings.TrimRight(blockRaw.String(), "\n")})
				block.Reset()
				blockRaw.Reset()
				inShellBlock = false
				continue
			}
			block.WriteString(line)
			continue
		}
		if strings.HasPrefix(trim, "```!") && !inFence {
			inShellBlock = true
			blockRaw.WriteString(line)
			continue
		}
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			inFence = !inFence
			continue
		}
		for _, m := range inlineShell.FindAllStringSubmatch(line, -1) {
			out = append(out, Injection{Kind: InjectShell, Text: m[3], Raw: m[2]})
		}
		if inFence {
			continue
		}
		for _, m := range fileRef.FindAllStringSubmatch(stripInlineCode(line), -1) {
			path := strings.TrimRight(m[3], ".,;:!?")
			if !strings.ContainsAny(path, "/.") || strings.HasPrefix(path, "agent-") || strings.Contains(path, "@") {
				continue
			}
			out = append(out, Injection{Kind: InjectFile, Text: path, Raw: "@" + path})
		}
	}
	return out
}

// stripInlineCode blanks `code spans` so references inside them are skipped.
func stripInlineCode(line string) string {
	b := []byte(line)
	in := false
	for i, c := range b {
		if c == '`' {
			in = !in
			continue
		}
		if in {
			b[i] = ' '
		}
	}
	return string(b)
}
