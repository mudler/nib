package claudeplugin

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Agent is a sub-agent definition: an agents/**.md file. The body is the
// system prompt.
type Agent struct {
	// Name is the agent name. Subfolders of agents/ add segments, and the
	// frontmatter name replaces only the last one: agents/review/sec.md is
	// "review:sec".
	Name          string
	QualifiedName string
	Description   string
	// Prompt is the system prompt, raw.
	Prompt string
	Path   string

	// Tools and DisallowedTools keep the Claude Code tool names as written,
	// including Task, Agent, Skill and TodoWrite. Map them with a
	// [ToolMap]. Empty Tools means "inherit all tools".
	Tools           []string
	DisallowedTools []string
	// Model is the model as written: a tier ("sonnet", "opus", "haiku"),
	// "inherit" or a model id. See [ModelTier].
	Model    string
	Effort   string
	Skills   []string
	MaxTurns int
	Color    string
	// Background, Memory and Isolation are passed through as written.
	Background string
	Memory     string
	Isolation  string

	// Ignored lists frontmatter fields that plugin agents may not use
	// (permissionMode, hooks, mcpServers, initialPrompt).
	Ignored     []string
	Frontmatter map[string]any
}

// ModelTier returns the tier of a model value: "sonnet", "opus", "haiku",
// "inherit" for an empty value or "inherit", or "" for anything else (a model
// id the host may resolve itself). The host maps a tier to a model.
func ModelTier(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch m {
	case "", "inherit":
		return "inherit"
	case "sonnet", "opus", "haiku":
		return m
	}
	return ""
}

var ignoredAgentFields = []string{"permissionMode", "hooks", "mcpServers", "initialPrompt"}

func (l *loader) loadAgents(m *manifest) {
	raw := m.Agents
	if len(raw) == 0 || string(raw) == "null" {
		if d := filepath.Join(l.p.Root, "agents"); isDir(d) {
			l.loadAgentDir(d)
		}
	} else {
		paths, err := pathList(raw)
		if err != nil {
			l.p.skip("agent", "agents", "manifest field: "+err.Error())
		}
		l.loadAgentPaths(paths)
	}
	l.loadAgentPaths(l.entryAgents)
}

func (l *loader) loadAgentPaths(paths []string) {
	for _, rel := range paths {
		abs, err := CheckPath(l.p.Root, rel, false)
		if err != nil {
			l.p.skip("agent", rel, err.Error())
			continue
		}
		switch {
		case isFile(abs) && strings.HasSuffix(abs, ".md"):
			l.loadAgentFile(abs, "")
		case isDir(abs):
			// The format accepts files only; read the directory anyway
			// and say so, since real plugins do this.
			l.p.skip("agent", rel, "agents paths must be .md files; directory read anyway")
			l.loadAgentDir(abs)
		default:
			l.p.skip("agent", rel, "path not found or not a .md file")
		}
	}
}

func (l *loader) loadAgentDir(dir string) {
	var files []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".md") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	for _, f := range files {
		rel, _ := filepath.Rel(dir, f)
		prefix := filepath.ToSlash(filepath.Dir(rel))
		if prefix == "." {
			prefix = ""
		}
		l.loadAgentFile(f, strings.ReplaceAll(prefix, "/", ":"))
	}
}

func (l *loader) loadAgentFile(path, prefix string) {
	data, err := os.ReadFile(path)
	if err != nil {
		l.p.skip("agent", l.rel(path), err.Error())
		return
	}
	fmRaw, body := SplitFrontmatter(data)
	fm, ferr := parseFrontmatter(fmRaw)
	if ferr != nil {
		// As in Claude Code: the agent loads with every field ignored.
		l.p.skip("agent", l.rel(path), ferr.Error()+"; all fields ignored")
		fm = frontmatter{}
	}
	name := fm.str("name")
	if name == "" || len(name) > 256 {
		name = strings.TrimSuffix(filepath.Base(path), ".md")
	}
	if prefix != "" {
		name = prefix + ":" + name
	}
	a := Agent{
		Name:            name,
		QualifiedName:   Qualify(l.p.Name, name),
		Description:     fm.str("description"),
		Prompt:          body,
		Path:            path,
		Tools:           fm.list("tools"),
		DisallowedTools: fm.list("disallowedTools"),
		Model:           fm.str("model"),
		Effort:          fm.str("effort"),
		Skills:          fm.list("skills"),
		MaxTurns:        fm.integer("maxTurns"),
		Color:           fm.str("color"),
		Background:      fm.str("background"),
		Memory:          fm.str("memory"),
		Isolation:       fm.str("isolation"),
		Frontmatter:     fm,
	}
	if a.Description == "" {
		a.Description = "Agent from " + l.p.Name + " plugin"
	}
	for _, k := range ignoredAgentFields {
		if fm.has(k) {
			a.Ignored = append(a.Ignored, k)
			l.p.skip("agent", a.QualifiedName, "plugin agents ignore the "+k+" field")
		}
	}
	l.p.Agents = append(l.p.Agents, a)
}
