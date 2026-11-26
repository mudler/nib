package plugin

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mudler/nib/slash"
	"github.com/mudler/nib/types"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		writeFile(t, filepath.Join(root, filepath.FromSlash(name)), body)
	}
}

func TestLoadClaudeManifestFull(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		".claude-plugin/plugin.json": `{"name": "demo", "version": "1.2.3", "description": "a claude plugin",
			"userConfig": {"region": {"type": "string", "title": "Region", "description": "r", "default": "eu"}}}`,
		"skills/s1/SKILL.md":        "---\nname: s1\ndescription: skill one\nallowed-tools: Bash, Read, TodoWrite\n---\nRun ${CLAUDE_SKILL_DIR}/run.sh in ${user_config.region}\n",
		"skills/group/s2/SKILL.md":  "---\ndescription: nested skill\n---\nbody two\n",
		"skills/manual/SKILL.md":    "---\ndescription: user only\ndisable-model-invocation: true\narguments: [target]\n---\nDeploy $target\n",
		"commands/review.md":        "---\ndescription: review the diff\nargument-hint: \"[pr]\"\n---\nReview {{ this }}: $ARGUMENTS from ${CLAUDE_PLUGIN_ROOT}\n",
		"commands/db/migrate.md":    "Migrate $0\n",
		"agents/team/researcher.md": "---\nname: researcher\ndescription: digs\ntools: Read, Grep, Task\nmodel: sonnet\nmaxTurns: 4\n---\nYou research.\n",
		".mcp.json":                 `{"mcpServers": {"srv": {"command": "mcp-srv", "args": ["${CLAUDE_PLUGIN_ROOT}/serve"]}, "remote": {"type": "http", "url": "https://mcp.example/${MISSING_VAR:-v1}", "headers": {"X": "y"}}, "sock": {"type": "ws", "url": "wss://x"}}}`,
		"hooks/hooks.json": `{"hooks": {
			"PreToolUse": [{"matcher": "Edit|Write", "hooks": [{"type": "command", "command": "\"${CLAUDE_PLUGIN_ROOT}/guard.sh\"", "timeout": 5}]}],
			"SessionStart": [{"matcher": "startup", "hooks": [{"type": "command", "command": "start.sh", "args": ["${CLAUDE_PLUGIN_ROOT}/a b"]}]}],
			"PreCompact": [{"hooks": [{"type": "command", "command": "ignored.sh"}]}],
			"Stop": [{"hooks": [{"type": "prompt", "prompt": "done?"}]}]}}`,
		".lsp.json": `{"go": {"command": "gopls", "extensionToLanguage": {".go": "go"}}}`,
	})

	m, err := loadClaudeManifest(dir)
	if err != nil {
		t.Fatalf("loadClaudeManifest: %v", err)
	}
	if m.Name != "demo" || m.Version != "1.2.3" || m.Description != "a claude plugin" || m.Claude() == nil {
		t.Fatalf("meta wrong: %+v", m)
	}

	skills := map[string]SkillSpec{}
	for _, s := range m.Skills {
		skills[s.Name] = s
	}
	s1, ok := skills["demo:s1"]
	if !ok || len(skills) != 2 || skills["demo:s2"].Dir != filepath.Join(dir, "skills", "group", "s2") {
		t.Fatalf("skills wrong: %+v", m.Skills)
	}
	if want := "Run " + filepath.Join(dir, "skills", "s1") + "/run.sh in eu\n"; s1.Instructions.Inline != want || !reflect.DeepEqual(s1.Tools, []string{"bash", "read"}) {
		t.Fatalf("s1 = %+v", s1)
	}

	cmds := map[string]types.CommandConfig{}
	for _, c := range m.Commands {
		cmds[c.Name] = c
	}
	if c := cmds["demo:review"]; c.Format != types.HookFormatClaude || c.Description != "review the diff [pr]" {
		t.Fatalf("review = %+v", c)
	}
	out, err := slash.Expand(cmds["demo:review"], "42")
	if err != nil || out != "Review {{ this }}: 42 from "+dir+"\n" {
		t.Fatalf("expand = %q, %v", out, err)
	}
	if out, _ := slash.Expand(cmds["demo:manual"], "prod"); out != "Deploy prod\n" {
		t.Fatalf("disable-model-invocation skill becomes a command: %q (%+v)", out, cmds)
	}
	if _, ok := cmds["demo:db:migrate"]; !ok {
		t.Fatalf("nested command missing: %+v", cmds)
	}

	if len(m.Agents) != 1 {
		t.Fatalf("agents = %+v", m.Agents)
	}
	a := m.Agents[0]
	if a.Name != "demo:team:researcher" || a.Model != "" || a.Iterations != 4 || !reflect.DeepEqual(a.Tools, []string{"read", "grep"}) {
		t.Fatalf("agent = %+v", a)
	}

	if s := m.MCPServers["srv"]; s.Command != "mcp-srv" || s.Args[0] != dir+"/serve" || s.Env["CLAUDE_PLUGIN_ROOT"] != dir {
		t.Fatalf("stdio mcp = %+v", s)
	}
	if s := m.MCPServers["remote"]; s.URL != "https://mcp.example/v1" || s.Transport != "http" || s.Headers["X"] != "y" {
		t.Fatalf("http mcp = %+v", s)
	}
	if _, ok := m.MCPServers["sock"]; ok {
		t.Fatal("ws is not supported by nib")
	}

	if len(m.Hooks) != 2 {
		t.Fatalf("hooks = %+v", m.Hooks)
	}
	for _, h := range m.Hooks {
		if h.Format != types.HookFormatClaude {
			t.Errorf("hook format = %q", h.Format)
		}
		switch h.Event {
		case "PreToolUse":
			if h.Matcher != "edit|write" || h.Timeout != 5 || h.Command != `"`+dir+`/guard.sh"` {
				t.Errorf("PreToolUse = %+v", h)
			}
			if !slices.ContainsFunc(h.Env, func(e string) bool { return strings.HasPrefix(e, "CLAUDE_PLUGIN_DATA=") }) ||
				!slices.Contains(h.Env, "CLAUDE_PLUGIN_OPTION_REGION=eu") {
				t.Errorf("env = %v", h.Env)
			}
		case "SessionStart":
			if h.Matcher != "startup" || h.Command != `'start.sh' '`+dir+`/a b'` {
				t.Errorf("SessionStart = %+v", h)
			}
		}
	}

	notes := strings.Join(m.Notes, "\n")
	for _, want := range []string{"TodoWrite", "Task", "model \"sonnet\"", "ws transport", "PreCompact", "prompt handlers", "lsp"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}
	if !m.Claude().RunsCode() {
		t.Error("RunsCode")
	}
}

func TestLoadClaudeFromSourceRecord(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"skills/x/SKILL.md": "---\ndescription: x\n---\nx\n"})
	if DetectFormat(dir) != FormatUnknown {
		t.Fatal("a bare skills dir is not a plugin")
	}
	if err := writeSourceRecord(dir, sourceRecord{Marketplace: "mkt", Entry: []byte(`{"name":"entry-only","source":"./x","description":"from entry"}`), SHA: "abc"}); err != nil {
		t.Fatal(err)
	}
	if DetectFormat(dir) != FormatClaude {
		t.Fatal("the source record marks a marketplace plugin")
	}
	m, err := LoadManifest(dir, "0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "entry-only" || m.Description != "from entry" || m.Claude().ID != "entry-only@mkt" || len(m.Skills) != 1 || m.Skills[0].Name != "entry-only:x" {
		t.Fatalf("manifest = %+v", m)
	}
}

func TestApplyClaudeNamespacing(t *testing.T) {
	base := t.TempDir()
	for _, p := range []struct{ name, cmd string }{{"one", "shared"}, {"two", "shared"}, {"two", "solo"}} {
		writeFiles(t, filepath.Join(PluginsDir(base), p.name), map[string]string{
			".claude-plugin/plugin.json":        `{"name":"` + p.name + `"}`,
			"commands/" + p.cmd + ".md":         "run " + p.cmd + " $ARGUMENTS\n",
			"skills/" + p.name + "-sk/SKILL.md": "---\ndescription: d\n---\nbody\n",
		})
	}
	reg, _ := LoadRegistry(base)
	reg.Upsert(Entry{Name: "one", Enabled: true})
	reg.Upsert(Entry{Name: "two", Enabled: true})
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	cfg := types.Config{Commands: []types.CommandConfig{{Name: "user", Prompt: "u"}}}
	if err := Apply(&cfg, base, "0.9.0"); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range cfg.Commands {
		names = append(names, c.Name)
	}
	slices.Sort(names)
	if want := []string{"one:shared", "solo", "two:shared", "two:solo", "user"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("commands = %v, want %v (bare alias only when unique)", names, want)
	}
	if s, ok := types.FindSkill(cfg.Skills, "one-sk"); !ok || s.Name != "one:one-sk" {
		t.Fatalf("bare skill lookup = %+v %v", s, ok)
	}
	if a := slash.Resolve("/one:shared now", cfg.Commands, cfg.Skills, nil); a.Kind != slash.KindSend || a.Text != "run shared now\n" {
		t.Fatalf("resolve = %+v", a)
	}
}
