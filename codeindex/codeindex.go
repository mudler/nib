package codeindex

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/msuozzo/bonsai"
)

const (
	maxFileSize  int64 = 2 << 20 // 2 MB
	maxFields    int   = 8
	maxDetailLen int   = 120
)

// Section labels an entry for the output skeleton.
type Section string

const (
	SectionPackage  Section = "Package"
	SectionImport   Section = "Import"
	SectionType     Section = "Type"
	SectionFunc     Section = "Function"
	SectionMethod   Section = "Method"
	SectionConst    Section = "Constant"
	SectionVar      Section = "Variable"
	SectionClass    Section = "Class"
	SectionTrait    Section = "Trait" // interface in Java, trait in Rust
	SectionImpl     Section = "Impl"
	SectionModule   Section = "Module"
	SectionMacro    Section = "Macro"
	SectionHeading  Section = "Heading"  // markdown headings
	SectionResource Section = "Resource" // terraform/HCL blocks
)

// Entry is one element of the file skeleton.
type Entry struct {
	Section   Section
	Name      string
	Detail    string // one-line signature or type info
	StartLine int    // 1-indexed
	EndLine   int    // 1-indexed
	Fields    []string
}

// Extractor walks a tree-sitter AST and produces entries.
type Extractor interface {
	Extract(root *bonsai.Node, src []byte) []Entry
}

type language struct {
	name      string
	exts      []string
	pool      *sync.Pool
	extractor Extractor
}

var (
	mu    sync.RWMutex
	byExt = map[string]*language{}
)

func register(name string, exts []string, newParser func() *bonsai.Parser, ext Extractor) {
	l := &language{
		name:      name,
		exts:      exts,
		pool:      &sync.Pool{New: func() any { return newParser() }},
		extractor: ext,
	}
	mu.Lock()
	defer mu.Unlock()
	for _, e := range exts {
		byExt[e] = l
	}
}

// SupportedExtensions returns file extensions with registered extractors.
func SupportedExtensions() []string {
	mu.RLock()
	defer mu.RUnlock()
	exts := make([]string, 0, len(byExt))
	for ext := range byExt {
		exts = append(exts, ext)
	}
	sort.Strings(exts)
	return exts
}

// Entries parses a source file and returns its raw skeleton entries. It is the
// structural core of Index: Index calls this and formats the result, while
// callers that want the entries themselves (the repo_map tool) use Entries
// directly. hasError reports whether the tree-sitter parse produced errors.
func Entries(path string) (entries []Entry, hasError bool, err error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, false, fmt.Errorf("index %s: %w", path, err)
	}
	if fi.Size() > maxFileSize {
		return nil, false, fmt.Errorf("index %s: file is %s, exceeds %s limit",
			path, humanBytes(fi.Size()), humanBytes(maxFileSize))
	}

	ext := strings.ToLower(filepath.Ext(path))
	mu.RLock()
	lang := byExt[ext]
	if lang == nil {
		// Fall back to filename for extension-less files like
		// "Dockerfile" or "Jenkinsfile".
		lang = byExt[strings.ToLower(filepath.Base(path))]
	}
	mu.RUnlock()
	if lang == nil {
		return nil, false, fmt.Errorf("index: no extractor for %s files (supported: %s)",
			ext, strings.Join(SupportedExtensions(), ", "))
	}

	src, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("index %s: %w", path, err)
	}

	p := lang.pool.Get().(*bonsai.Parser)
	defer lang.pool.Put(p)

	root, err := p.Parse(src)
	if err != nil {
		return nil, false, fmt.Errorf("index %s: parse: %w", path, err)
	}

	entries = lang.extractor.Extract(root, src)
	return entries, root.HasError(), nil
}

// Index parses a source file and returns its compact skeleton.
func Index(path string) (string, error) {
	entries, hasError, err := Entries(path)
	if err != nil {
		return "", err
	}
	return formatEntries(entries, hasError), nil
}

func formatEntries(entries []Entry, hasError bool) string {
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].StartLine < entries[j].StartLine
	})

	var b strings.Builder
	for _, e := range entries {
		lr := lineRange(e.StartLine, e.EndLine)
		detail := truncate(e.Detail, maxDetailLen)
		if len(e.Fields) > 0 {
			fmt.Fprintf(&b, "%s: %s [%s]\n", e.Section, detail, lr)
			shown := e.Fields
			if len(shown) > maxFields {
				shown = shown[:maxFields]
			}
			for _, f := range shown {
				fmt.Fprintf(&b, "  %s\n", truncate(f, maxDetailLen-2))
			}
			if len(e.Fields) > maxFields {
				fmt.Fprintf(&b, "  ... (%d more)\n", len(e.Fields)-maxFields)
			}
		} else {
			fmt.Fprintf(&b, "%s: %s [%s]\n", e.Section, detail, lr)
		}
	}

	if hasError {
		b.WriteString("\n(parse errors detected; some entries may be inaccurate)\n")
	}

	return b.String()
}

func lineRange(start, end int) string {
	if start == end {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d-%d", start, end)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
