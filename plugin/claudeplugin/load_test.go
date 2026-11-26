package claudeplugin

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func fullPlugin(t *testing.T) string {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".claude-plugin/plugin.json": `{
			"name": "deploy-tools", "version": "1.2.0", "description": "Deploy",
			"author": {"name": "Ex", "email": "e@example.com"}, "repository": {"url": "https://example.com/r"},
			"skills": ["./extra-skills/"],
			"agents": ["./custom/reviewer.md"],
			"mcpServers": {"api": {"type": "http", "url": "https://api.example/mcp", "headers": {"Authorization": "Bearer ${user_config.token}"}}},
			"lspServers": {"go": {"command": "gopls", "args": ["serve"], "extensionToLanguage": {".go": "go"}}},
			"userConfig": {
				"token": {"type": "string", "title": "Token", "description": "API token", "sensitive": true, "required": true},
				"tone": {"type": "string", "title": "Tone", "description": "Voice", "options": ["warm", "formal"], "default": "warm"},
				"retries": {"type": "number", "title": "Retries", "description": "n", "min": 0, "max": 5, "default": 2},
				"bad-key": {"type": "string", "title": "x", "description": "x"},
				"weird": {"type": "color", "title": "x", "description": "x"}
			}
		}`,
		"skills/review/SKILL.md":             "---\nname: review\ndescription: Review code\nallowed-tools: Read, Grep, Bash(git diff:*)\nargument-hint: \"[pr]\"\narguments: [pr]\ndisable-model-invocation: true\nmodel: haiku\n---\nReview PR $pr. Run ${CLAUDE_SKILL_DIR}/scripts/check.sh\n",
		"skills/review/scripts/check.sh*":    "#!/bin/sh\necho ok\n",
		"skills/group/deep/SKILL.md":         "---\ndescription: nested two levels\n---\nDeep.\n",
		"skills/group/deep/inner/SKILL.md":   "---\ndescription: must not load\n---\n",
		"skills/.hidden/SKILL.md":            "---\ndescription: hidden\n---\n",
		"extra-skills/SKILL.md":              "---\nname: extra\ndescription: extra root skill\n---\nExtra.\n",
		"skills/broken/SKILL.md":             "---\ndescription: Use this: when things break: always\n---\nbody\n",
		"commands/about.md":                  "---\ndescription: About\nallowed-tools: Read\n---\nExplain {{ .Template }} for $ARGUMENTS\n",
		"commands/db/migrate.md":             "---\ndescription: Migrate\n---\nStatus: !`git status`\nSee @docs/db.md\n",
		"agents/ignored-because-manifest.md": "---\nname: x\n---\n",
		"custom/reviewer.md":                 "---\nname: reviewer\ndescription: Reviews\ntools: Read, Grep, Task, TodoWrite\nmodel: sonnet\nmaxTurns: 7\npermissionMode: plan\nskills: [review]\n---\nYou review.\n",
		"hooks/hooks.json":                   `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"check.sh"}]}]}}`,
		".mcp.json":                          `{"local": {"command": "node", "args": ["${CLAUDE_PLUGIN_ROOT}/server.js"]}, "api": {"command": "replaced"}}`,
		"bin/deploy-tool*":                   "#!/bin/sh\n",
		"output-styles/terse.md":             "---\nname: terse\n---\nShort.\n",
		"themes/dark.json":                   "{}",
	})
	return root
}

func TestLoadFull(t *testing.T) {
	root := fullPlugin(t)
	p, err := Load(root, LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "deploy-tools" || p.ID != "deploy-tools" || p.Version != "1.2.0" || p.Author.Email != "e@example.com" || p.Repository != "https://example.com/r" {
		t.Errorf("metadata = %+v", p)
	}

	var names []string
	for _, s := range p.Skills {
		names = append(names, s.QualifiedName)
	}
	slices.Sort(names)
	if want := []string{"deploy-tools:broken", "deploy-tools:deep", "deploy-tools:extra", "deploy-tools:review"}; !reflect.DeepEqual(names, want) {
		t.Errorf("skills = %v, want %v", names, want)
	}
	for _, s := range p.Skills {
		switch s.Name {
		case "review":
			if s.Dir != filepath.Join(root, "skills", "review") || !s.DisableModelInvocation || s.Model != "haiku" ||
				!reflect.DeepEqual(s.AllowedTools, []string{"Read", "Grep", "Bash(git diff:*)"}) || !reflect.DeepEqual(s.Arguments, []string{"pr"}) || s.ArgumentHint != "[pr]" {
				t.Errorf("review skill = %+v", s)
			}
			body := ExpandContent(RenderArguments(s.Body, "42", s.Arguments), Vars{SkillDir: s.Dir})
			if want := "Review PR 42. Run " + filepath.Join(root, "skills", "review") + "/scripts/check.sh\n"; body != want {
				t.Errorf("rendered = %q, want %q", body, want)
			}
		case "broken":
			if s.Description != "Use this: when things break: always" {
				t.Errorf("lenient frontmatter: %q", s.Description)
			}
		}
	}
	if !hasSkip(p, "skill", "not valid YAML") {
		t.Error("broken frontmatter should be reported")
	}

	cmds := map[string]Command{}
	for _, c := range p.Commands {
		cmds[c.Name] = c
	}
	if c := cmds["about"]; c.QualifiedName != "deploy-tools:about" || RenderArguments(c.Body, "x", nil) != "Explain {{ .Template }} for x\n" {
		t.Errorf("about = %+v", c)
	}
	if c := cmds["db:migrate"]; c.QualifiedName != "deploy-tools:db:migrate" || len(c.Injections) != 2 {
		t.Errorf("db:migrate = %+v", c)
	}

	if len(p.Agents) != 1 {
		t.Fatalf("manifest agents replace agents/: got %+v", p.Agents)
	}
	a := p.Agents[0]
	if a.QualifiedName != "deploy-tools:reviewer" || ModelTier(a.Model) != "sonnet" || a.MaxTurns != 7 ||
		!reflect.DeepEqual(a.Tools, []string{"Read", "Grep", "Task", "TodoWrite"}) || !reflect.DeepEqual(a.Ignored, []string{"permissionMode"}) ||
		!reflect.DeepEqual(a.Skills, []string{"review"}) {
		t.Errorf("agent = %+v", a)
	}

	if s := p.MCPServers["api"]; s.Type != "http" || !s.IsRemote() {
		t.Errorf("plugin.json mcp must replace .mcp.json entry: %+v", s)
	}
	if s := p.MCPServers["local"]; s.Type != "stdio" || s.IsRemote() {
		t.Errorf("local = %+v", s)
	}
	exp, _ := p.MCPServers["local"].Expand(Vars{PluginRoot: root})
	if exp.Args[0] != filepath.Join(root, "server.js") {
		t.Errorf("expanded args = %v", exp.Args)
	}
	if l := p.LSPServers["go"]; l.Command != "gopls" || l.ExtensionToLanguage[".go"] != "go" {
		t.Errorf("lsp = %+v", l)
	}
	if len(p.Bin) != 1 || p.BinDir != filepath.Join(root, "bin") {
		t.Errorf("bin = %v %s", p.Bin, p.BinDir)
	}
	if len(p.OutputStyles) != 1 {
		t.Errorf("output styles = %v", p.OutputStyles)
	}
	if len(p.Hooks) != 1 || p.Hooks[0].Matcher != "Bash" {
		t.Errorf("hooks = %+v", p.Hooks)
	}
	if got := p.CodeReasons(); !reflect.DeepEqual(got, []string{CodeBin, CodeHookCommand, CodeLSP, CodeMCPStdio}) {
		t.Errorf("CodeReasons = %v", got)
	}
	if !p.HasShellInjections() {
		t.Error("db:migrate has a shell injection")
	}
	if len(p.UserConfig) != 3 || !p.UserConfig["token"].Sensitive {
		t.Errorf("userConfig = %+v", p.UserConfig)
	}
	if !hasSkip(p, "manifest", "userConfig.bad-key") || !hasSkip(p, "manifest", "userConfig.weird") || !hasSkip(p, "theme", "") {
		t.Errorf("skipped = %+v", p.Skipped)
	}
}

func TestUserConfigResolve(t *testing.T) {
	p, err := Load(fullPlugin(t), LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ResolveUserConfig(nil); err == nil {
		t.Error("required token missing must fail")
	}
	if _, err := p.ResolveUserConfig(map[string]any{"token": "x", "tone": "loud"}); err == nil {
		t.Error("tone outside options must fail")
	}
	if _, err := p.ResolveUserConfig(map[string]any{"token": "x", "retries": 9.0}); err == nil {
		t.Error("retries above max must fail")
	}
	vals, err := p.ResolveUserConfig(map[string]any{"token": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"token": "x", "tone": "warm", "retries": "2"}; !reflect.DeepEqual(vals, want) {
		t.Errorf("values = %v", vals)
	}
	if env := OptionEnv(vals); !reflect.DeepEqual(env, []string{"CLAUDE_PLUGIN_OPTION_RETRIES=2", "CLAUDE_PLUGIN_OPTION_TOKEN=x", "CLAUDE_PLUGIN_OPTION_TONE=warm"}) {
		t.Errorf("env = %v", env)
	}
	v := Vars{UserConfig: vals, Sensitive: p.SensitiveKeys()}
	if got, _ := p.MCPServers["api"].Expand(v); got.Headers["Authorization"] != "Bearer x" {
		t.Errorf("header = %q", got.Headers["Authorization"])
	}
}

func TestLoadPathRules(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeTree(t, outside, map[string]string{"evil/SKILL.md": "---\nname: evil\n---\n"})
	writeTree(t, root, map[string]string{
		".claude-plugin/plugin.json": `{"name":"p","skills":["../x","skills-no-dot"],"commands":"commands","agents":["./a/../../b.md"],"hooks":"./nope.json"}`,
	})
	p, err := Load(root, LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`".." or escapes`, `must start with "./"`} {
		found := false
		for _, s := range p.Skipped {
			if strings.Contains(s.Reason, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no skip with %q: %+v", want, p.Skipped)
		}
	}
	if len(p.Skills)+len(p.Commands)+len(p.Agents) != 0 {
		t.Errorf("nothing should load: %+v", p)
	}
	if _, err := CheckPath(root, "./ok/../../x", false); !errors.Is(err, ErrPathTraversal) {
		t.Errorf("CheckPath traversal: %v", err)
	}
	if got, err := CheckPath(root, ".", true); err != nil || got != root {
		t.Errorf("CheckPath dot: %q %v", got, err)
	}
}

func TestLoadRootSkillAndLayout(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"SKILL.md": "---\ndescription: single\n---\nOne skill.\n"})
	p, err := Load(root, LoadOptions{Name: "solo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Skills) != 1 || p.Skills[0].QualifiedName != "solo:solo" || p.Skills[0].Dir != root {
		t.Errorf("root skill = %+v", p.Skills)
	}
	if p.RunsCode() {
		t.Error("text-only")
	}

	empty := t.TempDir()
	if _, err := Load(empty, LoadOptions{}); !errors.Is(err, ErrNotPlugin) {
		t.Errorf("empty dir: %v", err)
	}
	bad := t.TempDir()
	writeTree(t, bad, map[string]string{".claude-plugin/plugin.json": `{"name":"a b"}`})
	if _, err := Load(bad, LoadOptions{}); err == nil {
		t.Error("name with a space must fail")
	}
}

func TestLoadAgentsRecursive(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".claude-plugin/plugin.json": `{"name":"mp"}`,
		"agents/top.md":              "---\ndescription: top\n---\nTop.\n",
		"agents/review/security.md":  "---\ndescription: sec\n---\nSec.\n",
		"agents/review/renamed.md":   "---\nname: audit\ndescription: a\n---\nA.\n",
		"agents/bad.md":              "---\nname: [unclosed\n---\nBody.\n",
		"commands/a/b/c.md":          "deep command\n",
	})
	p, err := Load(root, LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range p.Agents {
		got = append(got, a.QualifiedName)
	}
	slices.Sort(got)
	if want := []string{"mp:bad", "mp:review:audit", "mp:review:security", "mp:top"}; !reflect.DeepEqual(got, want) {
		t.Errorf("agents = %v, want %v", got, want)
	}
	if len(p.Commands) != 1 || p.Commands[0].Name != "a:b:c" {
		t.Errorf("commands = %+v", p.Commands)
	}
}

func TestLoadEntryStrictMerge(t *testing.T) {
	mk := func(strict string, manifest bool) (string, *Entry) {
		root := t.TempDir()
		files := map[string]string{
			"skills/base/SKILL.md": "---\ndescription: base\n---\n",
			"more/extra/SKILL.md":  "---\ndescription: extra\n---\n",
			"commands/one.md":      "one\n",
			"extra-cmds/two.md":    "two\n",
			"hooks/hooks.json":     `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"plugin-stop"}]}],"SessionStart":[{"hooks":[{"type":"command","command":"plugin-start"}]}]}}`,
			"servers/.keep":        "",
		}
		if manifest {
			files[".claude-plugin/plugin.json"] = `{"name":"real-name","version":"2.0.0"}`
		}
		writeTree(t, root, files)
		raw := `{"name":"entry-name","source":"./p","version":"1.0.0","description":"from entry",` + strict +
			`"skills":["./more"],"commands":["./extra-cmds/two.md"],"hooks":{"Stop":[{"hooks":[{"type":"command","command":"entry-stop"}]}]},` +
			`"lspServers":{"go":{"command":"gopls","extensionToLanguage":{".go":"go"}}}}`
		m, err := ParseMarketplace([]byte(`{"name":"mkt","owner":{"name":"o"},"plugins":[` + raw + `]}`))
		if err != nil {
			t.Fatal(err)
		}
		return root, &m.Plugins[0]
	}

	t.Run("no manifest: entry is the manifest", func(t *testing.T) {
		root, e := mk("", false)
		p, err := Load(root, LoadOptions{Entry: e, Marketplace: "mkt"})
		if err != nil {
			t.Fatal(err)
		}
		if p.Name != "entry-name" || p.ID != "entry-name@mkt" || p.Version != "1.0.0" || len(p.LSPServers) != 1 {
			t.Errorf("plugin = %+v", p)
		}
		if len(p.Skills) != 2 {
			t.Errorf("skills = %+v", p.Skills)
		}
		if len(p.Commands) != 1 || p.Commands[0].Name != "two" {
			t.Errorf("entry commands replace the default scan: %+v", p.Commands)
		}
	})

	t.Run("strict: append and replace hooks per event", func(t *testing.T) {
		root, e := mk("", true)
		p, err := Load(root, LoadOptions{Entry: e, Marketplace: "mkt"})
		if err != nil {
			t.Fatal(err)
		}
		if p.Name != "real-name" || p.ID != "entry-name@mkt" || p.Version != "2.0.0" || p.Description != "from entry" {
			t.Errorf("metadata = %+v", p)
		}
		if len(p.LSPServers) != 0 {
			t.Error("entry lspServers do not apply when plugin.json exists")
		}
		if len(p.Skills) != 2 || len(p.Commands) != 2 {
			t.Errorf("skills %d commands %d", len(p.Skills), len(p.Commands))
		}
		cmds := map[string]string{}
		for _, g := range p.Hooks {
			cmds[g.Event] = g.Handlers[0].Command
		}
		if cmds["Stop"] != "entry-stop" || cmds["SessionStart"] != "plugin-start" {
			t.Errorf("hooks = %v", cmds)
		}
	})

	t.Run("strict false with components conflicts", func(t *testing.T) {
		root, e := mk(`"strict":false,`, true)
		if _, err := Load(root, LoadOptions{Entry: e}); !errors.Is(err, ErrConflictingManifests) {
			t.Errorf("err = %v", err)
		}
	})
}
