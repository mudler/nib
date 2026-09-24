package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/codeindex"
)

const repoMapDefaultBudget = 3000
const repoMapMaxBudget = 8000

var repoMapSkipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true,
	"vendor": true, ".venv": true, "__pycache__": true,
	"dist": true, "build": true, "target": true, ".next": true,
}

type repoMapArgs struct {
	Path   string `json:"path,omitempty" jsonschema:"path to the directory to map (relative to the workspace or absolute); defaults to the workspace root"`
	Budget int    `json:"budget,omitempty" jsonschema:"optional token budget for the output (default 3000, max 8000)"`
}

type repoMapTool struct {
	workingDir   string
	resolvePath  func(string) string
}

type repoMapFileEntry struct {
	path     string
	entries  []codeindex.Entry
	defCount int
	size     int64
}

// Run resolves the path, collects source files, indexes each, ranks them, and
// renders a token-budgeted overview.
func (t *repoMapTool) Run(args map[string]any) (string, any, error) {
	path, _ := args["path"].(string)
	if path == "" {
		path = "."
	}
	resolved := t.resolvePath(path)

	budget, _ := args["budget"].(int)
	if v, ok := args["budget"].(float64); ok {
		budget = int(v)
	}
	if budget <= 0 {
		budget = repoMapDefaultBudget
	}
	if budget > repoMapMaxBudget {
		budget = repoMapMaxBudget
	}

	out, err := renderRepoMap(resolved, budget)
	if err != nil {
		return "repo_map failed: " + err.Error(), nil, nil
	}
	return out, nil, nil
}

// collectRepoMapFiles walks root, skipping repoMapSkipDirs and files whose
// extensions are not in codeindex.SupportedExtensions().
func collectRepoMapFiles(root string) ([]string, error) {
	supported := make(map[string]bool)
	for _, ext := range codeindex.SupportedExtensions() {
		supported[ext] = true
	}

	var files []string
	walkErr := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			// Skip paths we can't stat; don't abort the whole walk.
			return nil
		}
		if d.IsDir() {
			if p == root {
				return nil
			}
			if repoMapSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		base := strings.ToLower(filepath.Base(p))
		// Extensionless files like "Dockerfile" fall back to the basename,
		// mirroring codeindex's own lookup.
		if supported[ext] || (ext == "" && supported[base]) {
			files = append(files, p)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Strings(files)
	return files, nil
}

// filterRepoMapEntries drops the package/import/heading sections that don't
// contribute to a codebase overview, keeping types, functions, methods,
// classes, traits, impls, modules, macros, constants, vars and resources.
func filterRepoMapEntries(entries []codeindex.Entry) []codeindex.Entry {
	var kept []codeindex.Entry
	for _, e := range entries {
		switch e.Section {
		case codeindex.SectionPackage, codeindex.SectionImport, codeindex.SectionHeading:
			continue
		}
		kept = append(kept, e)
	}
	return kept
}

// renderRepoMap indexes every supported source file under root, ranks them, and
// emits a token-budgeted overview.
func renderRepoMap(root string, budget int) (string, error) {
	files, err := collectRepoMapFiles(root)
	if err != nil {
		return "", fmt.Errorf("collect files: %w", err)
	}

	var entries []repoMapFileEntry
	for _, f := range files {
		fi, statErr := os.Stat(f)
		if statErr != nil {
			continue
		}
		raw, _, indexErr := codeindex.Entries(f)
		if indexErr != nil {
			continue
		}
		kept := filterRepoMapEntries(raw)
		if len(kept) == 0 {
			continue
		}
		entries = append(entries, repoMapFileEntry{
			path:     f,
			entries:  kept,
			defCount: len(kept),
			size:     fi.Size(),
		})
	}

	// Rank by definition count (more first), then file size (larger first).
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].defCount != entries[j].defCount {
			return entries[i].defCount > entries[j].defCount
		}
		return entries[i].size > entries[j].size
	})

	byteBudget := budget * 4
	var b strings.Builder
	shown := 0
	for _, fe := range entries {
		block := renderRepoMapFile(fe)
		if shown > 0 && len(b.String())+len(block)+1 > byteBudget {
			break
		}
		if shown > 0 {
			b.WriteString("\n")
		}
		b.WriteString(block)
		shown++
	}

	omitted := len(entries) - shown
	if omitted > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "... (%d more file(s) not shown)\n", omitted)
	}

	return b.String(), nil
}

// renderRepoMapFile formats a single file's entry block:
//
//	path/to/file.go
//	  Function: Foo (line 10)
//	  Type: Bar (line 25)
func renderRepoMapFile(fe repoMapFileEntry) string {
	sorted := make([]codeindex.Entry, len(fe.entries))
	copy(sorted, fe.entries)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].StartLine < sorted[j].StartLine
	})

	var b strings.Builder
	b.WriteString(fe.path)
	b.WriteString("\n")
	for _, e := range sorted {
		fmt.Fprintf(&b, "  %s: %s (line %d)\n", e.Section, e.Name, e.StartLine)
	}
	return b.String()
}

func repoMapToolDefinition(workingDir string, resolvePath func(string) string) cogito.ToolDefinitionInterface {
	return cogito.NewToolDefinition[map[string]any](
		&repoMapTool{workingDir: workingDir, resolvePath: resolvePath},
		repoMapArgs{},
		"repo_map",
		"Return a bird's-eye map of a codebase: a token-budgeted tree of the definitions "+
			"(types, functions, methods, classes, constants, variables) in every indexable source file, "+
			"each with its file and starting line. It costs a fraction of reading every file.\n\n"+
			"Use it once, early, to learn which file owns a symbol without grepping, then read or index "+
			"that file for the details. When the map would exceed its token budget it drops the files "+
			"with the fewest definitions and notes how many it omitted.\n\n"+
			"Supported files: "+strings.Join(codeindex.SupportedExtensions(), " ")+".",
	)
}
