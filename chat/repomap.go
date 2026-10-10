package chat

import (
	"context"
	"fmt"
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
	workerFactory func(context.Context) repoMapWorker
	workingDir    string
	resolvePath   func(string) string
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
	return t.RunContext(context.Background(), args)
}

// RunContext is dispatched by Cogito when the calling session is cancelled.
func (t *repoMapTool) RunContext(ctx context.Context, args map[string]any) (string, any, error) {
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

	factory := t.workerFactory
	if factory == nil {
		factory = newRepoMapWorker
	}
	out, err := renderRepoMapWith(ctx, resolved, budget, factory, repoMapLimitsDefault)
	if err != nil {
		return "repo_map failed: " + err.Error(), nil, nil
	}
	return out, nil, nil
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

// renderRepoMap is the legacy non-context entry point.
func renderRepoMap(root string, budget int) (string, error) {
	return renderRepoMapWith(context.Background(), root, budget, newRepoMapWorker, repoMapLimitsDefault)
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
			"Work is capped at 10s, 1000 attempted files, 16 MiB input and 20000 discovery entries; ignores and safe regular-file checks apply. Partial maps explain limits; narrow path to continue.\n\n"+
			"Supported files: "+strings.Join(codeindex.SupportedExtensions(), " ")+".",
	)
}
