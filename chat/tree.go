package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/codeindex"
)

const (
	treeDefaultDepth  = 2
	treeDefaultPerDir = 12
	treeMaxDepth      = 4
	treeMaxPerDir     = 60
)

var treeSkipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true,
	"node_modules": true, "dist": true, "build": true,
	"target": true, "bin": true, "obj": true,
	".venv": true, "venv": true, "__pycache__": true,
	".next": true, ".nuxt": true, ".cache": true,
	".idea": true, ".vscode": true,
}

type treeArgs struct {
	Path   string `json:"path" jsonschema:"optional directory to list (relative to the workspace or absolute; default: workspace root)"`
	Depth  int    `json:"depth,omitempty" jsonschema:"optional max depth of nesting to show (default 2, max 4)"`
	PerDir int    `json:"per_dir,omitempty" jsonschema:"optional max children per directory before truncation (default 12, max 60)"`
}

type treeTool struct {
	resolvePath func(string) string
}

func (t *treeTool) Run(args map[string]any) (string, any, error) {
	path, _ := args["path"].(string)
	depth := treeDefaultDepth
	if d, ok := args["depth"].(float64); ok && d > 0 {
		depth = int(d)
	}
	if depth > treeMaxDepth {
		depth = treeMaxDepth
	}
	perDir := treeDefaultPerDir
	if p, ok := args["per_dir"].(float64); ok && p > 0 {
		perDir = int(p)
	}
	if perDir > treeMaxPerDir {
		perDir = treeMaxPerDir
	}
	resolved := t.resolvePath(path)
	if resolved == "" {
		resolved = "."
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "tree error: " + err.Error(), nil, nil
	}
	if !info.IsDir() {
		return "tree error: not a directory: " + resolved, nil, nil
	}
	out, err := renderTree(resolved, depth, perDir)
	if err != nil {
		return "tree failed: " + err.Error(), nil, nil
	}
	return out, nil, nil
}

func renderTree(root string, maxDepth, perDir int) (string, error) {
	var b strings.Builder
	rel, err := filepath.Rel(".", root)
	if err != nil {
		rel = root
	}
	if rel == "." {
		rel = filepath.Base(root)
		if rel == "" || rel == "." {
			rel = "."
		}
	}
	b.WriteString(rel + "/\n")
	renderDirChildren(&b, root, "", 1, maxDepth, perDir)
	return strings.TrimRight(b.String(), "\n"), nil
}

func renderDirChildren(b *strings.Builder, dir, prefix string, depth, maxDepth, perDir int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		b.WriteString(prefix + "(unreadable: " + err.Error() + ")\n")
		return
	}
	var kept []os.DirEntry
	for _, e := range entries {
		if e.IsDir() && treeSkipDirs[e.Name()] {
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) == 0 {
		b.WriteString(prefix + "(empty directory)\n")
		return
	}
	sort.SliceStable(kept, func(i, j int) bool {
		ti, _ := entryModTime(filepath.Join(dir, kept[i].Name()))
		tj, _ := entryModTime(filepath.Join(dir, kept[j].Name()))
		if ti.Equal(tj) {
			if kept[i].IsDir() != kept[j].IsDir() {
				return kept[i].IsDir()
			}
			return kept[i].Name() < kept[j].Name()
		}
		return ti.After(tj)
	})
	original := len(kept)
	if original > perDir {
		kept = kept[:perDir]
	}
	last := len(kept) - 1
	for i, e := range kept {
		isLast := i == last
		conn := "├── "
		if isLast {
			conn = "└── "
		}
		name := e.Name()
		full := filepath.Join(dir, name)
		if e.IsDir() {
			b.WriteString(prefix + conn + name + "/\n")
			if depth < maxDepth {
				childPrefix := prefix
				if isLast {
					childPrefix += "    "
				} else {
					childPrefix += "│   "
				}
				renderDirChildren(b, full, childPrefix, depth+1, maxDepth, perDir)
			}
		} else {
			size := humanSize(fullSize(full))
			line := prefix + conn + name + "  " + size
			if syms := fileSymbols(full); len(syms) > 0 {
				line += "  [" + strings.Join(syms, ", ") + "]"
			}
			b.WriteString(line + "\n")
		}
	}
	if original > perDir {
		b.WriteString(prefix + "└── … " + fmt.Sprintf("%d more\n", original-perDir))
	}
}

func entryModTime(path string) (time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

func fullSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fk", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1fM", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.1fG", float64(n)/(1024*1024*1024))
	}
}

var treeSupportedExts map[string]bool

func init() {
	treeSupportedExts = make(map[string]bool, len(codeindex.SupportedExtensions()))
	for _, e := range codeindex.SupportedExtensions() {
		treeSupportedExts[e] = true
	}
}

// fileSymbols returns up to 5 top-level symbol names from a source file,
// using codeindex to parse it. Returns nil for non-source files or on
// error. This gives the LLM semantic context inline in the tree view
// without a separate index call.
func fileSymbols(path string) []string {
	ext := strings.ToLower(filepath.Ext(path))
	if !treeSupportedExts[ext] {
		return nil
	}
	entries, _, err := codeindex.Entries(path)
	if err != nil || len(entries) == 0 {
		return nil
	}
	var syms []string
	for _, e := range entries {
		switch e.Section {
		case codeindex.SectionPackage, codeindex.SectionImport, codeindex.SectionHeading:
			continue
		}
		name := e.Name
		if name == "" {
			name = e.Detail
		}
		if name == "" {
			continue
		}
		syms = append(syms, name)
		if len(syms) >= 5 {
			break
		}
	}
	return syms
}

func treeToolDefinition(resolvePath func(string) string) cogito.ToolDefinitionInterface {
	return cogito.NewToolDefinition[map[string]any](
		&treeTool{resolvePath: resolvePath}, treeArgs{},
		"tree",
		"Render a shallow directory listing of a path so you can see the layout before searching. "+
			"Shows up to 2 levels of nesting by default, with at most 12 entries per directory "+
			"(both configurable), file sizes, and gitignore/build artifacts hidden. "+
			"Source files are annotated with their top symbols (types, functions, methods) in brackets. "+
			"Use it to orient yourself in an unfamiliar directory instead of guessing filenames with glob or grep. "+
			"For the contents of a single source file, read it (or index it if large) — tree lists names, not contents.",
	)
}
