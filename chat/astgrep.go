package chat

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/mudler/cogito"
)

type astGrepArgs struct {
	Pat  string `json:"pat" jsonschema:"AST pattern with metavariables ($NAME captures one node, $_ matches any single node, $$$NAME captures zero or more nodes). Metavariables must be uppercase."`
	Path string `json:"path,omitempty" jsonschema:"file, directory, or glob to search (default: workspace root)"`
	Skip int    `json:"skip,omitempty" jsonschema:"offset for paginating results (default 0)"`
}

type astGrepTool struct {
	resolvePath func(string) string
}

func (t *astGrepTool) Run(args map[string]any) (string, any, error) {
	pat, _ := args["pat"].(string)
	if pat == "" {
		return "ast_grep error: 'pat' is required", nil, nil
	}
	path, _ := args["path"].(string)
	if path == "" {
		path = "."
	}
	resolved := t.resolvePath(path)
	skip := 0
	if v, ok := args["skip"].(float64); ok {
		skip = int(v)
	}

	if _, err := exec.LookPath("ast-grep"); err != nil {
		return "ast_grep error: ast-grep binary not found on PATH. Install it from https://ast-grep.github.io/", nil, nil
	}

	cmd := exec.Command("ast-grep", "run", "--json=stream", pat, resolved)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "ast_grep error: " + strings.TrimSpace(string(ee.Stderr)), nil, nil
		}
		return "ast_grep error: " + err.Error(), nil, nil
	}

	type sgMatch struct {
		Text string                 `json:"text"`
		File string                 `json:"file"`
		Line int                    `json:"line"`
		Vars map[string]interface{} `json:"metaVariables"`
	}
	var matches []sgMatch
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m sgMatch
		if err := json.Unmarshal([]byte(line), &m); err == nil {
			matches = append(matches, m)
		}
	}

	total := len(matches)
	if skip > total {
		skip = total
	}
	rest := matches[skip:]
	limit := 50
	if len(rest) > limit {
		rest = rest[:limit]
	}

	var b strings.Builder
	for _, m := range rest {
		fmt.Fprintf(&b, "%s:%d\n  %s\n", m.File, m.Line, strings.TrimSpace(m.Text))
		if len(m.Vars) > 0 {
			var kv []string
			for k, v := range m.Vars {
				kv = append(kv, fmt.Sprintf("%s=%v", k, v))
			}
			fmt.Fprintf(&b, "  meta: %s\n", strings.Join(kv, ", "))
		}
		b.WriteString("\n")
	}
	if total > 0 {
		showFrom := skip + 1
		showTo := skip + len(rest)
		fmt.Fprintf(&b, "%d matches (showing %d-%d", total, showFrom, showTo)
		if showTo < total {
			fmt.Fprintf(&b, "; use skip=%d for more", showTo)
		}
		b.WriteString(")\n")
	} else {
		b.WriteString("no matches\n")
	}
	return b.String(), nil, nil
}

func astGrepToolDefinition(resolvePath func(string) string) cogito.ToolDefinitionInterface {
	return cogito.NewToolDefinition[map[string]any](
		&astGrepTool{resolvePath: resolvePath}, astGrepArgs{},
		"ast_grep",
		"Structural code search using ast-grep. Pass an AST pattern with metavariables "+
			"($NAME captures one node, $_ matches any single node, $$$NAME captures zero or more) "+
			"and it finds every structural match across the codebase. Use it when grep matches too much "+
			"or too little because the same identifier appears in different contexts. "+
			"Requires the ast-grep binary on PATH (https://ast-grep.github.io/).",
	)
}
