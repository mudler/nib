package claudeplugin

import (
	"reflect"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a b  c", []string{"a", "b", "c"}},
		{`"hello world" second`, []string{"hello world", "second"}},
		{`'it''s' x`, []string{"its", "x"}},
		{`a\ b c`, []string{"a b", "c"}},
		{`"say \"hi\""`, []string{`say "hi"`}},
		{`"unclosed rest`, []string{"unclosed rest"}},
		{`""`, []string{""}},
	}
	for _, tt := range tests {
		if got := SplitArgs(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("SplitArgs(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRenderArguments(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		args  string
		names []string
		want  string
	}{
		{"all", "Fix $ARGUMENTS now", "issue 12", nil, "Fix issue 12 now"},
		{"indexed", "$0 then $1", `"a b" c`, nil, "a b then c"},
		{"indexed long form", "$ARGUMENTS[1]!", "x y", nil, "y!"},
		{"missing index stays", "$0 and $2", "only", nil, "only and $2"},
		{"named", "Move $component from $from to $to", "Button react vue", []string{"component", "from", "to"}, "Move Button from react to vue"},
		{"missing named empty", "[$a][$b]", "1", []string{"a", "b"}, "[1][]"},
		{"escape", `costs \$1 for $0`, "me", nil, "costs $1 for me"},
		{"shell vars untouched", "echo $HOME ${PATH}", "", nil, "echo $HOME ${PATH}"},
		{"no placeholder appends", "Review the diff.\n", "--strict", nil, "Review the diff.\n\nARGUMENTS: --strict\n"},
		{"no placeholder no args", "Review.\n", "", nil, "Review.\n"},
		{"go template braces kept", "Use {{ .Field }} and $ARGUMENTS", "x", nil, "Use {{ .Field }} and x"},
		{"ARGUMENTS prefix of identifier", "$ARGUMENTSX", "a", nil, "aX"},
		{"dollar alone", "$ 5 and $", "", nil, "$ 5 and $"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RenderArguments(tt.body, tt.args, tt.names); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExpandContentAndConfig(t *testing.T) {
	v := Vars{
		PluginRoot: "/p/root",
		PluginData: "/p/data",
		ProjectDir: "/proj",
		SkillDir:   "/p/root/skills/x",
		UserConfig: map[string]string{"endpoint": "https://api", "token": "s3cret"},
		Sensitive:  map[string]bool{"token": true},
		Env: func(k string) (string, bool) {
			if k == "SET" {
				return "yes", true
			}
			if k == "EMPTY" {
				return "", true
			}
			return "", false
		},
	}
	content := []struct{ in, want string }{
		{"${CLAUDE_PLUGIN_ROOT}/scripts/a.sh", "/p/root/scripts/a.sh"},
		{"${CLAUDE_PLUGIN_DATA} ${CLAUDE_PROJECT_DIR} ${CLAUDE_SKILL_DIR}", "/p/data /proj /p/root/skills/x"},
		{"url ${user_config.endpoint}", "url https://api"},
		{"tok ${user_config.token}", "tok [sensitive option token]"},
		{"${user_config.unknown} ${SET} ${HOME:-x}", "${user_config.unknown} ${SET} ${HOME:-x}"},
		{"${CLAUDE_SESSION_ID}", "${CLAUDE_SESSION_ID}"},
	}
	for _, tt := range content {
		if got := ExpandContent(tt.in, v); got != tt.want {
			t.Errorf("ExpandContent(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	config := []struct {
		in, want string
		missing  []string
	}{
		{"Bearer ${user_config.token}", "Bearer s3cret", nil},
		{"${SET}", "yes", nil},
		{"http://127.0.0.1:${PORT:-27182}/mcp", "http://127.0.0.1:27182/mcp", nil},
		{"${EMPTY:-fallback}", "fallback", nil},
		{"${EMPTY}", "", nil},
		{"Bearer ${NOPE}", "Bearer ", []string{"NOPE"}},
		{"${CLAUDE_PLUGIN_ROOT}/bin", "/p/root/bin", nil},
	}
	for _, tt := range config {
		var missing []string
		if got := ExpandConfig(tt.in, v, &missing); got != tt.want || !reflect.DeepEqual(missing, tt.missing) {
			t.Errorf("ExpandConfig(%q) = %q missing %v, want %q missing %v", tt.in, got, missing, tt.want, tt.missing)
		}
	}
	// Without Env, ${VAR} stays.
	if got := ExpandConfig("${SET}", Vars{}, nil); got != "${SET}" {
		t.Errorf("nil Env: got %q", got)
	}
}

func TestParseInjections(t *testing.T) {
	body := "Status: !`git status --short`\n" +
		"KEY=!`not run`\n" +
		"Read @src/main.go and @README.md, then @someone.\n" +
		"Mail me@example.com, see `@not/this.go`.\n" +
		"```!\nnode --version\ngit log -1\n```\n" +
		"```python\n@app.route(\"/x\")\n```\n"
	got := ParseInjections(body)
	want := []Injection{
		{Kind: InjectShell, Text: "git status --short", Raw: "!`git status --short`"},
		{Kind: InjectFile, Text: "src/main.go", Raw: "@src/main.go"},
		{Kind: InjectFile, Text: "README.md", Raw: "@README.md"},
		{Kind: InjectShellBlock, Text: "node --version\ngit log -1", Raw: "```!\nnode --version\ngit log -1\n```"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
}

func TestSplitToolList(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"Read, Grep, Bash(git add:*)", []string{"Read", "Grep", "Bash(git add:*)"}},
		{"Read Grep Glob", []string{"Read", "Grep", "Glob"}},
		{"Bash(git status:*) Bash(git diff:*)", []string{"Bash(git status:*)", "Bash(git diff:*)"}},
		{"Bash(a, b), Read", []string{"Bash(a, b)", "Read"}},
		{"", nil},
	}
	for _, tt := range tests {
		if got := SplitToolList(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("SplitToolList(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestToolMap(t *testing.T) {
	m := ToolMap{"Bash": "bash", "Read": "read", "Edit": "edit", "MultiEdit": "edit", "TodoWrite": ""}
	mapped, unmapped := m.Map([]string{"Read", "Bash(git:*)", "Edit", "MultiEdit", "Task", "TodoWrite", "mcp__x__y", "Task"})
	if !reflect.DeepEqual(mapped, []string{"read", "bash", "edit", "mcp__x__y"}) {
		t.Errorf("mapped = %v", mapped)
	}
	if !reflect.DeepEqual(unmapped, []string{"Task", "TodoWrite"}) {
		t.Errorf("unmapped = %v", unmapped)
	}
	if r := m.Reverse(); r["edit"] != "Edit" || r["bash"] != "Bash" {
		t.Errorf("Reverse = %v", r)
	}
}

func TestModelTier(t *testing.T) {
	for in, want := range map[string]string{"sonnet": "sonnet", "Opus": "opus", "haiku": "haiku", "": "inherit", "inherit": "inherit", "some-model-id": ""} {
		if got := ModelTier(in); got != want {
			t.Errorf("ModelTier(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDataDirName(t *testing.T) {
	if got := DataDirName("my-plugin@my.market"); got != "my-plugin-my-market" {
		t.Errorf("got %q", got)
	}
}
