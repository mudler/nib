package cmd

import "strings"

// defaultProgramName is the name every emitted script falls back to, matching
// app.Options.name().
const defaultProgramName = "nib"

// GetInitScript returns the shell integration script for the given shell,
// wired to invoke programName. An empty programName means "nib", so the zero
// value reproduces standalone nib's script byte for byte.
//
// programName may be more than one word, because an embedder ships nib as a
// subcommand ("local-ai chat") rather than as a binary. The script therefore
// renders it in three different shapes, which coincide only for a plain
// one-word name: see initScriptCommand, initScriptIdent and
// initScriptDisplayName.
func GetInitScript(shell, programName string) string {
	var script string
	switch shell {
	case "zsh":
		script = zshInitScript
	case "bash":
		script = bashInitScript
	case "fish":
		script = fishInitScript
	default:
		return ""
	}
	return strings.NewReplacer(
		"{{cmd}}", initScriptCommand(programName),
		"{{ident}}", initScriptIdent(programName),
		"{{name}}", initScriptDisplayName(programName),
	).Replace(script)
}

// initScriptCommand renders the program name as something the emitted script
// can actually run.
//
// The words stay separate words. Quoting the name as a unit would make the
// shell look for a single command literally called "local-ai chat", so a
// two-word name has to be interpolated as a command plus its first argument,
// exactly as the user types it.
//
// Each word is quoted only when it needs to be, which is what keeps the
// standalone "nib" output byte-identical while still refusing to paste an
// arbitrary embedder-supplied string into a command position.
func initScriptCommand(programName string) string {
	words := strings.Fields(programName)
	if len(words) == 0 {
		return defaultProgramName
	}
	quoted := make([]string, 0, len(words))
	for _, w := range words {
		quoted = append(quoted, shellQuoteWord(w))
	}
	return strings.Join(quoted, " ")
}

// shellQuoteWord returns word unchanged when every byte in it is safe to leave
// bare, and single-quoted otherwise. The bare set is deliberately narrow:
// anything outside it is quoted rather than reasoned about, including the
// characters that mean something to only one of the three shells.
//
// An embedded single quote is escaped by the POSIX close-escape-reopen idiom,
// which fish accepts as well: a backslash-escaped quote outside quotes is a
// literal one there too.
func shellQuoteWord(word string) string {
	bare := true
	for i := 0; i < len(word); i++ {
		if !isBareShellByte(word[i]) {
			bare = false
			break
		}
	}
	if bare {
		return word
	}
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
}

// isBareShellByte reports whether c can appear unquoted in a command word in
// zsh, bash and fish alike. '=' is excluded because a leading assignment is not
// a command, and '~' because it expands.
func isBareShellByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '_', '-', '.', '/', '+', ':', '@', ',':
		return true
	}
	return false
}

// initScriptIdent turns the program name into the fragment the widget function
// is named after. A function name is an identifier, not a command line: it
// cannot hold the space in "local-ai chat", and a hyphen, while all three
// shells happen to accept it today, is not worth relying on inside a `bind -x`
// string. So every byte outside [A-Za-z0-9_] collapses to a single underscore,
// with leading and trailing ones dropped, and "local-ai chat" names
// __local_ai_chat_widget.
func initScriptIdent(programName string) string {
	var b strings.Builder
	pendingSep := false
	for _, r := range programName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			if pendingSep && b.Len() > 0 {
				b.WriteByte('_')
			}
			pendingSep = false
			b.WriteRune(r)
		default:
			pendingSep = true
		}
	}
	if b.Len() == 0 {
		return defaultProgramName
	}
	return b.String()
}

// initScriptDisplayName is the name for the script's comments: the words
// joined by single spaces, unquoted, because prose is all it ends up in.
// Collapsing whitespace is also what keeps a name containing a newline from
// ending a comment line and turning the rest of the name into code.
func initScriptDisplayName(programName string) string {
	name := strings.Join(strings.Fields(blankControlChars(programName)), " ")
	if name == "" {
		return defaultProgramName
	}
	return name
}

// blankControlChars turns the control characters strings.Fields does not treat
// as whitespace into spaces, so they become word separators like the rest
// rather than reaching a generated comment.
func blankControlChars(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}

const zshInitScript = `# {{name}} shell integration for zsh
# Add this to your ~/.zshrc:
#   eval "$({{cmd}} --init zsh)"

__{{ident}}_widget() {
  local output
  # Save the current buffer
  local saved_buffer="$BUFFER"
  local saved_cursor="$CURSOR"

  # open {{name}} (inline drop-down; tmux popup when in tmux)
  # The TUI writes to /dev/tty directly, stdout captures only the final output
  output=$({{cmd}} --height 50%)
  local ret=$?

  # If {{name}} output a command, insert it
  if [[ -n "$output" ]]; then
    BUFFER="${saved_buffer:0:$saved_cursor}${output}${saved_buffer:$saved_cursor}"
    CURSOR=$((saved_cursor + ${#output}))
  fi

  zle reset-prompt
  return $ret
}

zle -N __{{ident}}_widget
bindkey '^ ' __{{ident}}_widget  # Ctrl+Space
`

const bashInitScript = `# {{name}} shell integration for bash
# Add this to your ~/.bashrc:
#   eval "$({{cmd}} --init bash)"

__{{ident}}_widget() {
  local output
  local saved_line="$READLINE_LINE"
  local saved_point="$READLINE_POINT"

  # open {{name}} (inline drop-down; tmux popup when in tmux)
  # The TUI writes to /dev/tty directly, stdout captures only the final output
  output=$({{cmd}} --height 50%)

  # If {{name}} output a command, insert it
  if [[ -n "$output" ]]; then
    READLINE_LINE="${saved_line:0:$saved_point}${output}${saved_line:$saved_point}"
    READLINE_POINT=$((saved_point + ${#output}))
  fi
}

# Bind Ctrl+Space
bind -x '"\C- ": __{{ident}}_widget'
`

const fishInitScript = `# {{name}} shell integration for fish
# Add this to your ~/.config/fish/config.fish:
#   {{cmd}} --init fish | source

function __{{ident}}_widget
  # open {{name}} (inline drop-down; tmux popup when in tmux)
  # The TUI writes to /dev/tty directly, stdout captures only the final output
  set -l output ({{cmd}} --height 50%)

  if test -n "$output"
    commandline -i "$output"
  end

  commandline -f repaint
end

bind \c\  __{{ident}}_widget  # Ctrl+Space
`
