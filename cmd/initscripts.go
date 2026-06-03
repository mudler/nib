package cmd

// getInitScript returns the shell integration script for the given shell
func GetInitScript(shell string) string {
	switch shell {
	case "zsh":
		return zshInitScript
	case "bash":
		return bashInitScript
	case "fish":
		return fishInitScript
	default:
		return ""
	}
}

const zshInitScript = `# nib shell integration for zsh
# Add this to your ~/.zshrc:
#   eval "$(nib --init zsh)"

__nib_widget() {
  local output
  # Save the current buffer
  local saved_buffer="$BUFFER"
  local saved_cursor="$CURSOR"

  # open nib (inline drop-down; tmux popup when in tmux)
  # The TUI writes to /dev/tty directly, stdout captures only the final output
  output=$(nib --height 50%)
  local ret=$?

  # If nib output a command, insert it
  if [[ -n "$output" ]]; then
    BUFFER="${saved_buffer:0:$saved_cursor}${output}${saved_buffer:$saved_cursor}"
    CURSOR=$((saved_cursor + ${#output}))
  fi

  zle reset-prompt
  return $ret
}

zle -N __nib_widget
bindkey '^ ' __nib_widget  # Ctrl+Space
`

const bashInitScript = `# nib shell integration for bash
# Add this to your ~/.bashrc:
#   eval "$(nib --init bash)"

__nib_widget() {
  local output
  local saved_line="$READLINE_LINE"
  local saved_point="$READLINE_POINT"

  # open nib (inline drop-down; tmux popup when in tmux)
  # The TUI writes to /dev/tty directly, stdout captures only the final output
  output=$(nib --height 50%)

  # If nib output a command, insert it
  if [[ -n "$output" ]]; then
    READLINE_LINE="${saved_line:0:$saved_point}${output}${saved_line:$saved_point}"
    READLINE_POINT=$((saved_point + ${#output}))
  fi
}

# Bind Ctrl+Space
bind -x '"\C- ": __nib_widget'
`

const fishInitScript = `# nib shell integration for fish
# Add this to your ~/.config/fish/config.fish:
#   nib --init fish | source

function __nib_widget
  # open nib (inline drop-down; tmux popup when in tmux)
  # The TUI writes to /dev/tty directly, stdout captures only the final output
  set -l output (nib --height 50%)

  if test -n "$output"
    commandline -i "$output"
  end

  commandline -f repaint
end

bind \c\  __nib_widget  # Ctrl+Space
`
