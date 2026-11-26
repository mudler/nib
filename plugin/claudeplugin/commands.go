package claudeplugin

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Command is a slash command: a commands/*.md file or an entry of the
// manifest "commands" map. Commands take the same frontmatter as skills.
type Command struct {
	// Name is the command name. A subdirectory of commands/ adds a segment:
	// commands/db/migrate.md is "db:migrate".
	Name string
	// QualifiedName is "<plugin>:<name>".
	QualifiedName string
	Description   string
	// Body is the prompt, raw. Render it with [RenderArguments] and
	// [ExpandContent].
	Body string
	// Path is the source file, empty for inline content.
	Path string

	ArgumentHint           string
	Arguments              []string
	AllowedTools           []string
	Model                  string
	DisableModelInvocation bool

	Injections  []Injection
	Frontmatter map[string]any
}

// commandMapEntry is one value of the manifest "commands" object map.
type commandMapEntry struct {
	Source       string   `json:"source"`
	Content      string   `json:"content"`
	Description  string   `json:"description"`
	ArgumentHint string   `json:"argumentHint"`
	Model        string   `json:"model"`
	AllowedTools []string `json:"allowedTools"`
}

func (l *loader) loadCommands(m *manifest) {
	raw := m.Commands
	if len(raw) == 0 || string(raw) == "null" {
		if d := filepath.Join(l.p.Root, "commands"); isDir(d) {
			l.loadCommandDir(d, true)
		}
	} else if trimmed := strings.TrimSpace(string(raw)); strings.HasPrefix(trimmed, "{") {
		l.loadCommandMap(raw)
	} else {
		paths, err := pathList(raw)
		if err != nil {
			l.p.skip(SkipInvalidField, "command", "commands", "manifest field: "+err.Error())
		}
		l.loadCommandPaths(paths)
	}
	l.loadCommandPaths(l.entryCommands)
}

func (l *loader) loadCommandPaths(paths []string) {
	for _, rel := range paths {
		abs, err := CheckPath(l.p.Root, rel, false)
		if err != nil {
			l.p.skip(SkipInvalidPath, "command", rel, err.Error())
			continue
		}
		switch {
		case isDir(abs):
			l.loadCommandDir(abs, true)
		case isFile(abs):
			l.loadCommandFile(abs, strings.TrimSuffix(filepath.Base(abs), ".md"))
		default:
			l.p.skip(SkipNotFound, "command", rel, "path not found")
		}
	}
}

func (l *loader) loadCommandDir(dir string, nested bool) {
	var files []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != dir && (!nested || strings.HasPrefix(d.Name(), ".")) {
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
		name := strings.ReplaceAll(filepath.ToSlash(strings.TrimSuffix(rel, ".md")), "/", ":")
		l.loadCommandFile(f, name)
	}
}

func (l *loader) loadCommandFile(path, name string) {
	data, err := os.ReadFile(path)
	if err != nil {
		l.p.skip(SkipReadError, "command", l.rel(path), err.Error())
		return
	}
	c := l.parseCommand(name, data, path)
	l.p.Commands = append(l.p.Commands, c)
}

func (l *loader) parseCommand(name string, data []byte, path string) Command {
	fmRaw, body := SplitFrontmatter(data)
	fm, ferr := parseFrontmatter(fmRaw)
	if ferr != nil {
		l.p.skip(SkipInvalidFrontmatter, "command", l.rel(path), ferr.Error())
	}
	c := Command{
		Name:          name,
		QualifiedName: Qualify(l.p.Name, name),
		Description:   fm.str("description"),
		Body:          body,
		Path:          path,
		ArgumentHint:  fm.str("argument-hint"),
		Arguments:     fm.list("arguments"),
		AllowedTools:  fm.list("allowed-tools"),
		Model:         fm.str("model"),
		Injections:    ParseInjections(body),
		Frontmatter:   fm,
	}
	c.DisableModelInvocation, _ = fm.boolean("disable-model-invocation")
	return c
}

func (l *loader) loadCommandMap(raw json.RawMessage) {
	var entries map[string]commandMapEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		l.p.skip(SkipInvalidField, "command", "commands", "manifest field: "+err.Error())
		return
	}
	names := make([]string, 0, len(entries))
	for k := range entries {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		e := entries[name]
		if (e.Source == "") == (e.Content == "") {
			l.p.skip(SkipInvalidField, "command", name, "set exactly one of source and content")
			continue
		}
		var c Command
		if e.Source != "" {
			abs, err := CheckPath(l.p.Root, e.Source, false)
			if err != nil {
				l.p.skip(SkipInvalidPath, "command", name, err.Error())
				continue
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				l.p.skip(SkipReadError, "command", name, err.Error())
				continue
			}
			c = l.parseCommand(name, data, abs)
		} else {
			c = l.parseCommand(name, []byte(e.Content), "")
		}
		if e.Description != "" {
			c.Description = e.Description
		}
		if e.ArgumentHint != "" {
			c.ArgumentHint = e.ArgumentHint
		}
		if e.Model != "" {
			c.Model = e.Model
		}
		if len(e.AllowedTools) > 0 {
			c.AllowedTools = e.AllowedTools
		}
		l.p.Commands = append(l.p.Commands, c)
	}
}
