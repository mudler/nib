package claudeplugin

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Skill is a SKILL.md file.
type Skill struct {
	// Name is the skill name: the frontmatter name, or the directory name.
	Name string
	// QualifiedName is "<plugin>:<name>", the name users and the model use.
	QualifiedName string
	Description   string
	WhenToUse     string
	// Body is the markdown after the frontmatter, raw.
	Body string
	// Dir is the skill directory, the value of ${CLAUDE_SKILL_DIR}. Bundled
	// scripts and references live under it.
	Dir string
	// Path is the SKILL.md file.
	Path string

	ArgumentHint string
	// Arguments are the names for $name substitution, in position order.
	Arguments              []string
	DisableModelInvocation bool
	// UserInvocable is false when the skill is hidden from the / menu.
	UserInvocable bool
	// AllowedTools and DisallowedTools keep the Claude Code tool names and
	// rules as written, such as "Bash(git add:*)". Map them with a [ToolMap].
	AllowedTools    []string
	DisallowedTools []string
	// Model is the model or tier for the turn, as written ("sonnet",
	// "inherit", a model id). See [ModelTier].
	Model   string
	Effort  string
	Context string // "fork" runs the skill in a sub-agent
	Agent   string // sub-agent type when Context is "fork"
	Shell   string
	Paths   []string
	// HasHooks is true when the frontmatter declares hooks. They are not
	// parsed; see Plugin.Skipped.
	HasHooks bool

	Injections []Injection
	// Frontmatter holds every frontmatter key, for fields this struct does
	// not model.
	Frontmatter map[string]any
}

// skillDirs returns every directory under dir that holds a SKILL.md, without
// descending into a skill directory. Hidden directories and node_modules are
// skipped.
func skillDirs(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		base := d.Name()
		if p != dir && (strings.HasPrefix(base, ".") || base == "node_modules") {
			return filepath.SkipDir
		}
		if isFile(filepath.Join(p, "SKILL.md")) {
			out = append(out, p)
			if p != dir {
				return filepath.SkipDir
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func (l *loader) loadSkills(m *manifest) {
	var dirs []string
	add := func(ds ...string) { dirs = append(dirs, ds...) }

	entrySkills, entryReplaces := l.entrySkillPaths()
	switch {
	case entryReplaces:
		for _, rel := range entrySkills {
			l.addSkillPath(rel, add)
		}
	default:
		defaultDir := filepath.Join(l.p.Root, "skills")
		if isDir(defaultDir) {
			for _, d := range skillDirs(defaultDir) {
				if d != defaultDir {
					add(d)
				}
			}
		}
		paths, err := pathList(m.Skills)
		if err != nil {
			l.p.skip(SkipInvalidField, "skill", "skills", "manifest field: "+err.Error())
		}
		paths = append(paths, entrySkills...)
		for _, rel := range paths {
			l.addSkillPath(rel, add)
		}
		if !isDir(defaultDir) && len(paths) == 0 && isFile(filepath.Join(l.p.Root, "SKILL.md")) {
			add(l.p.Root)
		}
	}

	seen := map[string]bool{}
	for _, d := range dirs {
		if seen[d] {
			continue
		}
		seen[d] = true
		l.loadSkill(d)
	}
}

// addSkillPath adds the skills under a manifest path: a directory that holds
// SKILL.md itself is one skill, any other directory is scanned.
func (l *loader) addSkillPath(rel string, add func(...string)) {
	abs, err := CheckPath(l.p.Root, rel, true)
	if err != nil {
		l.p.skip(SkipInvalidPath, "skill", rel, err.Error())
		return
	}
	if !isDir(abs) {
		l.p.skip(SkipNotFound, "skill", rel, "path not found or not a directory")
		return
	}
	add(skillDirs(abs)...)
}

func (l *loader) loadSkill(dir string) {
	path := filepath.Join(dir, "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		l.p.skip(SkipReadError, "skill", l.rel(path), err.Error())
		return
	}
	fmRaw, body := SplitFrontmatter(data)
	fm, ferr := parseFrontmatter(fmRaw)
	if ferr != nil {
		l.p.skip(SkipInvalidFrontmatter, "skill", l.rel(path), ferr.Error())
	}
	name := fm.str("name")
	if name == "" {
		name = filepath.Base(dir)
		if dir == l.p.Root {
			name = l.p.Name
		}
	}
	s := Skill{
		Name:            name,
		QualifiedName:   Qualify(l.p.Name, name),
		Description:     fm.str("description"),
		WhenToUse:       fm.str("when_to_use"),
		Body:            body,
		Dir:             dir,
		Path:            path,
		ArgumentHint:    fm.str("argument-hint"),
		Arguments:       fm.list("arguments"),
		UserInvocable:   true,
		AllowedTools:    fm.list("allowed-tools"),
		DisallowedTools: fm.list("disallowed-tools"),
		Model:           fm.str("model"),
		Effort:          fm.str("effort"),
		Context:         fm.str("context"),
		Agent:           fm.str("agent"),
		Shell:           fm.str("shell"),
		Paths:           fm.list("paths"),
		HasHooks:        fm.has("hooks"),
		Injections:      ParseInjections(body),
		Frontmatter:     fm,
	}
	s.DisableModelInvocation, _ = fm.boolean("disable-model-invocation")
	if v, ok := fm.boolean("user-invocable"); ok {
		s.UserInvocable = v
	}
	if s.HasHooks {
		l.p.skip(SkipIgnoredField, "skill", s.QualifiedName, "skill frontmatter hooks are not parsed")
	}
	l.p.Skills = append(l.p.Skills, s)
}
