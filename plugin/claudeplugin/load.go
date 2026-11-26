package claudeplugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ManifestPath is the manifest location inside a plugin directory.
const ManifestPath = ".claude-plugin/plugin.json"

// ErrNotPlugin is returned by [Load] for a directory that has neither a
// manifest, a marketplace entry, nor any component in the standard layout.
var ErrNotPlugin = errors.New("not a plugin: no .claude-plugin/plugin.json and no components")

// ErrConflictingManifests is returned when a plugin has its own plugin.json,
// its marketplace entry sets "strict": false, and the entry declares
// components too.
var ErrConflictingManifests = errors.New("conflicting manifests: both plugin.json and marketplace entry specify components")

// manifest is plugin.json, or a marketplace entry used as one. Component
// fields keep their raw JSON since each takes several shapes.
type manifest struct {
	Name         string          `json:"name"`
	DisplayName  string          `json:"displayName"`
	Version      flexString      `json:"version"`
	Description  string          `json:"description"`
	Author       json.RawMessage `json:"author"`
	Homepage     flexString      `json:"homepage"`
	Repository   json.RawMessage `json:"repository"`
	License      string          `json:"license"`
	Keywords     []string        `json:"keywords"`
	Skills       json.RawMessage `json:"skills"`
	Commands     json.RawMessage `json:"commands"`
	Agents       json.RawMessage `json:"agents"`
	Hooks        json.RawMessage `json:"hooks"`
	MCPServers   json.RawMessage `json:"mcpServers"`
	LSPServers   json.RawMessage `json:"lspServers"`
	OutputStyles json.RawMessage `json:"outputStyles"`
	Workflows    json.RawMessage `json:"workflows"`
	Themes       json.RawMessage `json:"themes"`
	Monitors     json.RawMessage `json:"monitors"`
	Experimental struct {
		Themes   json.RawMessage `json:"themes"`
		Monitors json.RawMessage `json:"monitors"`
	} `json:"experimental"`
	Channels   json.RawMessage `json:"channels"`
	UserConfig json.RawMessage `json:"userConfig"`
}

// flexString decodes a JSON string or number as a string.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = flexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err == nil {
		*f = flexString(n.String())
		return nil
	}
	return nil
}

// hasComponents reports whether the entry declares any of the component
// fields that strict mode governs.
func (m *manifest) hasComponents() bool {
	for _, r := range []json.RawMessage{m.Commands, m.Agents, m.Skills, m.Hooks, m.OutputStyles, m.Themes, m.Experimental.Themes} {
		if len(r) > 0 && string(r) != "null" {
			return true
		}
	}
	return false
}

// LoadOptions tunes [Load].
type LoadOptions struct {
	// Entry is the marketplace entry the plugin was installed from. It
	// serves as the manifest when the plugin has no plugin.json, and is
	// merged with plugin.json according to its strict flag otherwise.
	Entry *Entry
	// Marketplace is the marketplace name, used for Plugin.ID.
	Marketplace string
	// Name is the fallback plugin name when neither plugin.json nor Entry
	// gives one. Empty uses the directory name.
	Name string
}

type loader struct {
	p             *Plugin
	entrySkills   []string
	entryReplaces bool
	entryCommands []string
	entryAgents   []string
	entryHooks    json.RawMessage
}

// HasLayout reports whether dir looks like a plugin: it has a manifest or a
// component in the standard layout.
func HasLayout(dir string) bool {
	if isFile(filepath.Join(dir, ManifestPath)) {
		return true
	}
	for _, d := range []string{"commands", "agents", "hooks"} {
		if isDir(filepath.Join(dir, d)) {
			return true
		}
	}
	return isFile(filepath.Join(dir, ".mcp.json")) || isFile(filepath.Join(dir, ".lsp.json"))
}

// Load parses the plugin in dir. It returns an error only when the plugin
// cannot be loaded at all (no manifest and no entry, invalid plugin.json, a
// strict-mode conflict, an invalid name). Problems with single components
// are listed in Plugin.Skipped.
func Load(dir string, opts LoadOptions) (*Plugin, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if !isDir(root) {
		return nil, fmt.Errorf("plugin directory %s: not found", dir)
	}
	l := &loader{p: &Plugin{Root: root}}

	var m manifest
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ManifestPath)))
	hasManifest := err == nil
	switch {
	case hasManifest:
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", ManifestPath, err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}

	if e := opts.Entry; e != nil {
		var em manifest
		if len(e.Raw) > 0 {
			if err := json.Unmarshal(e.Raw, &em); err != nil {
				return nil, fmt.Errorf("marketplace entry %q: %w", e.Name, err)
			}
		}
		if err := l.applyEntry(&m, &em, hasManifest, e); err != nil {
			return nil, err
		}
	} else if !hasManifest && !HasLayout(root) && !isDir(filepath.Join(root, "skills")) && !isFile(filepath.Join(root, "SKILL.md")) {
		return nil, ErrNotPlugin
	}

	name := strings.TrimSpace(m.Name)
	if name == "" {
		name = opts.Name
	}
	if name == "" {
		name = filepath.Base(root)
	}
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	p := l.p
	p.Name = name
	p.ID = name
	if opts.Marketplace != "" {
		id := name
		if opts.Entry != nil && opts.Entry.Name != "" {
			id = opts.Entry.Name
		}
		p.ID = id + "@" + opts.Marketplace
	}
	p.DisplayName = m.DisplayName
	p.Version = string(m.Version)
	p.Description = m.Description
	p.Author = decodePerson(m.Author)
	p.Homepage = string(m.Homepage)
	p.Repository = decodeRepository(m.Repository)
	p.License = m.License
	p.Keywords = m.Keywords
	if e := opts.Entry; e != nil {
		// Display fields of the entry win over plugin.json; version does not.
		if e.DisplayName != "" {
			p.DisplayName = e.DisplayName
		}
		if e.Description != "" {
			p.Description = e.Description
		}
		if p.Version == "" {
			p.Version = e.Version
		}
	}

	l.loadUserConfig(m.UserConfig)
	l.loadSkills(&m)
	l.loadCommands(&m)
	l.loadAgents(&m)
	l.loadHooks(&m)
	l.loadMCP(&m)
	l.loadLSP(&m)
	l.loadBin()
	l.loadOutputStyles(&m)
	l.noteUnsupported(&m)
	return p, nil
}

// applyEntry combines a marketplace entry with plugin.json.
func (l *loader) applyEntry(m, em *manifest, hasManifest bool, e *Entry) error {
	rootSource := e.Source.Kind == SourceRelative && (e.Source.Path == "." || e.Source.Path == "./")
	switch {
	case !hasManifest:
		*m = *em
		if m.Name == "" {
			m.Name = e.Name
		}
		l.entrySkills, _ = pathList(m.Skills)
		m.Skills = nil
	case e.Strict == nil || *e.Strict:
		l.entrySkills, _ = pathList(em.Skills)
		l.entryCommands, _ = pathList(em.Commands)
		l.entryAgents, _ = pathList(em.Agents)
		if len(em.Hooks) > 0 && string(em.Hooks) != "null" {
			if strings.HasPrefix(strings.TrimSpace(string(em.Hooks)), "{") {
				l.entryHooks = em.Hooks
			} else {
				l.p.skip("hook", "marketplace entry", "hook files are not yet supported in a marketplace entry")
			}
		}
		if len(em.OutputStyles) > 0 && string(em.OutputStyles) != "null" {
			m.OutputStyles = em.OutputStyles
		}
	default:
		if em.hasComponents() {
			return fmt.Errorf("plugin %s: %w", e.Name, ErrConflictingManifests)
		}
	}
	l.entryReplaces = rootSource && len(l.entrySkills) > 0
	return nil
}

func (l *loader) entrySkillPaths() ([]string, bool) { return l.entrySkills, l.entryReplaces }

func (l *loader) loadOutputStyles(m *manifest) {
	paths, err := pathList(m.OutputStyles)
	if err != nil {
		l.p.skip("manifest", "outputStyles", err.Error())
	}
	if len(paths) == 0 {
		if d := filepath.Join(l.p.Root, "output-styles"); isDir(d) {
			paths = []string{"./output-styles"}
		}
	}
	for _, rel := range paths {
		abs, err := CheckPath(l.p.Root, rel, false)
		if err != nil {
			l.p.skip("output-style", rel, err.Error())
			continue
		}
		if isFile(abs) {
			l.p.OutputStyles = append(l.p.OutputStyles, abs)
			continue
		}
		entries, _ := os.ReadDir(abs)
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				l.p.OutputStyles = append(l.p.OutputStyles, filepath.Join(abs, e.Name()))
			}
		}
	}
}

// noteUnsupported records components the package does not model.
func (l *loader) noteUnsupported(m *manifest) {
	set := func(r json.RawMessage) bool { return len(r) > 0 && string(r) != "null" }
	if set(m.Monitors) || set(m.Experimental.Monitors) || isFile(filepath.Join(l.p.Root, "monitors", "monitors.json")) {
		l.p.skip("monitor", "monitors", "monitors are not supported")
	}
	if set(m.Themes) || set(m.Experimental.Themes) || isDir(filepath.Join(l.p.Root, "themes")) {
		l.p.skip("theme", "themes", "themes are not supported")
	}
	if set(m.Workflows) || isDir(filepath.Join(l.p.Root, "workflows")) {
		l.p.skip("workflow", "workflows", "workflows are not supported")
	}
	if set(m.Channels) {
		l.p.skip("channel", "channels", "channels are not supported")
	}
	if isFile(filepath.Join(l.p.Root, "CLAUDE.md")) {
		l.p.skip("manifest", "CLAUDE.md", "a CLAUDE.md at the plugin root is not loaded as context")
	}
}

// rel returns path relative to the plugin root, for messages.
func (l *loader) rel(path string) string {
	if r, err := filepath.Rel(l.p.Root, path); err == nil {
		return filepath.ToSlash(r)
	}
	return path
}

// ValidateName checks a plugin or marketplace name: non-empty, with no
// spaces, '@', ':', path separators or control characters, and not "." or
// "..".
func ValidateName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("invalid plugin name %q", name)
	}
	for _, r := range name {
		if r == ' ' || r == '@' || r == ':' || r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
			return fmt.Errorf("invalid plugin name %q: no spaces, '@', ':', path separators or control characters", name)
		}
	}
	return nil
}

// pathList decodes a path or an array of paths.
func pathList(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, fmt.Errorf("expected a path or a list of paths")
	}
	return many, nil
}

func decodePerson(raw json.RawMessage) *Person {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var p Person
	if err := json.Unmarshal(raw, &p); err == nil {
		return &p
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && s != "" {
		return &Person{Name: s}
	}
	return nil
}

func decodeRepository(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var o struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(raw, &o)
	return o.URL
}
