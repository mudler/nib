package cmd

import (
	"strings"
	"testing"
)

var initShells = []string{"zsh", "bash", "fish"}

// The line each shell's widget uses to actually run nib. Getting the program
// name into the comments and missing this one would ship a widget that still
// calls a binary the embedder's users do not have.
func invocationLine(t *testing.T, shell, command string) string {
	t.Helper()
	switch shell {
	case "zsh", "bash":
		return "output=$(" + command + " --height 50%)"
	case "fish":
		return "set -l output (" + command + " --height 50%)"
	}
	t.Fatalf("unknown shell %q", shell)
	return ""
}

// The default has to stay exactly what standalone nib has always emitted: the
// scripts are pasted into people's rc files and re-emitted on every shell
// start, so a stray placeholder or a renamed widget is a broken keybinding.
func TestInitScriptDefaultNameIsUnchanged(t *testing.T) {
	for _, shell := range initShells {
		t.Run(shell, func(t *testing.T) {
			script := GetInitScript(shell, "")
			if script == "" {
				t.Fatalf("GetInitScript(%q, \"\") returned nothing", shell)
			}
			if strings.Contains(script, "{{") || strings.Contains(script, "}}") {
				t.Fatalf("an unexpanded placeholder survived into the script:\n%s", script)
			}
			for _, want := range []string{
				"# nib shell integration for " + shell,
				invocationLine(t, shell, "nib"),
				"__nib_widget",
			} {
				if !strings.Contains(script, want) {
					t.Fatalf("script for %s is missing %q:\n%s", shell, want, script)
				}
			}
		})
	}
}

// Empty means "nib", the same defaulting app.Options.name() does, so a caller
// that has no name to pass gets standalone nib's script rather than a script
// with a hole in it.
func TestInitScriptEmptyNameIsTheDefaultName(t *testing.T) {
	for _, shell := range initShells {
		if got, want := GetInitScript(shell, ""), GetInitScript(shell, "nib"); got != want {
			t.Fatalf("GetInitScript(%q, \"\") differs from the explicit \"nib\":\n%s", shell, got)
		}
	}
}

// The embedder case, and the reason for the whole change: LocalAI ships nib as
// `local-ai chat`, two words. The widget must call both of them.
func TestInitScriptUsesAMultiWordProgramName(t *testing.T) {
	for _, shell := range initShells {
		t.Run(shell, func(t *testing.T) {
			script := GetInitScript(shell, "local-ai chat")

			if want := invocationLine(t, shell, "local-ai chat"); !strings.Contains(script, want) {
				t.Fatalf("script for %s does not run %q:\n%s", shell, want, script)
			}
			// Quoting the name as one word would have the shell look for a
			// command with a space in its name, which is the obvious wrong way
			// to make an interpolated string "safe".
			if strings.Contains(script, `'local-ai chat'`) || strings.Contains(script, `"local-ai chat"`) {
				t.Fatalf("the command is quoted as a single word, so it names no command:\n%s", script)
			}
			if strings.Contains(script, "nib") {
				t.Fatalf("script for %s still names the standalone binary:\n%s", shell, script)
			}
		})
	}
}

// A function name is an identifier, and "local-ai chat" is not one. Every place
// the widget's name appears has to agree on the reduced form, or the script
// defines one function and binds another.
func TestInitScriptWidgetNameIsAnIdentifier(t *testing.T) {
	const widget = "__local_ai_chat_widget"
	perShell := map[string][]string{
		"zsh":  {widget + "() {", "zle -N " + widget, "bindkey '^ ' " + widget},
		"bash": {widget + "() {", `bind -x '"\C- ": ` + widget + `'`},
		"fish": {"function " + widget, "bind \\c\\  " + widget},
	}
	for shell, wants := range perShell {
		t.Run(shell, func(t *testing.T) {
			script := GetInitScript(shell, "local-ai chat")
			for _, want := range wants {
				if !strings.Contains(script, want) {
					t.Fatalf("script for %s is missing %q:\n%s", shell, want, script)
				}
			}
		})
	}
}

func TestInitScriptIdent(t *testing.T) {
	cases := []struct{ name, want string }{
		{"nib", "nib"},
		{"local-ai chat", "local_ai_chat"},
		{"local-ai  chat", "local_ai_chat"}, // runs of separators collapse
		{" local-ai chat ", "local_ai_chat"},
		{"my.prog v2", "my_prog_v2"},
		{"/usr/bin/thing", "usr_bin_thing"},
		{"", "nib"},
		{"---", "nib"}, // nothing usable left
	}
	for _, c := range cases {
		got := initScriptIdent(c.name)
		if got != c.want {
			t.Fatalf("initScriptIdent(%q) = %q, want %q", c.name, got, c.want)
		}
		if !isShellIdentifier(got) {
			t.Fatalf("initScriptIdent(%q) = %q, which is not a shell identifier", c.name, got)
		}
	}
}

func isShellIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_',
			r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// The program name reaches a command position, so a word that is not plain has
// to be quoted there rather than pasted. Every word still stays its own word.
func TestInitScriptQuotesWordsThatNeedIt(t *testing.T) {
	cases := []struct{ name, want string }{
		{"nib", "nib"},
		{"local-ai chat", "local-ai chat"},
		{"/opt/local-ai/bin/local-ai chat", "/opt/local-ai/bin/local-ai chat"},
		{"prog; rm -rf /", `'prog;' rm -rf /`},
		{"prog $(id)", `prog '$(id)'`},
		{"it's chat", `'it'\''s' chat`},
	}
	for _, c := range cases {
		if got := initScriptCommand(c.name); got != c.want {
			t.Fatalf("initScriptCommand(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// A name carrying a newline would otherwise end the comment it is written into
// and leave the rest of the name sitting in the script as code, so the script
// must come out with the same number of lines a plain name produces.
func TestInitScriptCommentsStayOnOneLine(t *testing.T) {
	plain := GetInitScript("zsh", "prog")
	sneaky := GetInitScript("zsh", "evil\nzle -N stolen")
	if got, want := strings.Count(sneaky, "\n"), strings.Count(plain, "\n"); got != want {
		t.Fatalf("a newline in the program name added lines (%d, want %d):\n%s", got, want, sneaky)
	}
}

func TestGetInitScriptUnknownShellIsEmpty(t *testing.T) {
	if got := GetInitScript("tcsh", "local-ai chat"); got != "" {
		t.Fatalf("GetInitScript(\"tcsh\", ...) = %q, want empty", got)
	}
}
