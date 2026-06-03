package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestLastSuggestedCommand(t *testing.T) {
	cases := []struct {
		name     string
		messages []ChatMessage
		want     string
	}{
		{
			name:     "no messages",
			messages: nil,
			want:     "",
		},
		{
			name: "fenced block with language tag",
			messages: []ChatMessage{
				{Role: "user", Content: "find big files"},
				{Role: "assistant", Content: "Here you go:\n\n```bash\nfind . -type f -size +100M\n```\n"},
			},
			want: "find . -type f -size +100M",
		},
		{
			name: "fenced block without language tag",
			messages: []ChatMessage{
				{Role: "assistant", Content: "```\nls -la\n```"},
			},
			want: "ls -la",
		},
		{
			name: "multiple blocks returns the last",
			messages: []ChatMessage{
				{Role: "assistant", Content: "first:\n```\nls\n```\nor better:\n```\nls -la\n```"},
			},
			want: "ls -la",
		},
		{
			name: "uses the last assistant message",
			messages: []ChatMessage{
				{Role: "assistant", Content: "```\necho one\n```"},
				{Role: "user", Content: "no, the other one"},
				{Role: "assistant", Content: "```\necho two\n```"},
			},
			want: "echo two",
		},
		{
			name: "ignores tool and agent messages after the assistant",
			messages: []ChatMessage{
				{Role: "assistant", Content: "```\nuname -a\n```"},
				{Role: "tool", Name: "bash", Content: "Linux ..."},
			},
			want: "uname -a",
		},
		{
			name: "no fence, single-line answer is used verbatim",
			messages: []ChatMessage{
				{Role: "assistant", Content: "  git status --short  "},
			},
			want: "git status --short",
		},
		{
			name: "single line wrapped in inline backticks is unwrapped",
			messages: []ChatMessage{
				{Role: "assistant", Content: "`ls -la`"},
			},
			want: "ls -la",
		},
		{
			name: "no fence, multi-line prose yields nothing",
			messages: []ChatMessage{
				{Role: "assistant", Content: "You can do this in a few ways.\nFirst, try one thing.\nThen another."},
			},
			want: "",
		},
		{
			name: "multi-line fenced block is preserved",
			messages: []ChatMessage{
				{Role: "assistant", Content: "```bash\nset -e\nls -la\n```"},
			},
			want: "set -e\nls -la",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lastSuggestedCommand(tc.messages); got != tc.want {
				t.Fatalf("lastSuggestedCommand() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCtrlYYanksCommand verifies Ctrl+Y on an idle session sets the model's
// Output() to the last suggested command and quits, so the shell widget can
// insert it at the prompt.
func TestCtrlYYanksCommand(t *testing.T) {
	m := Model{
		sessionReady: true,
		cancel:       func() {},
		messages: []ChatMessage{
			{Role: "assistant", Content: "```\nfind . -type f -size +100M\n```"},
		},
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	mn := next.(Model)
	if !mn.quitting {
		t.Fatal("Ctrl+Y should quit so the shell can insert the command")
	}
	if got := mn.Output(); got != "find . -type f -size +100M" {
		t.Fatalf("Output() = %q, want the suggested command", got)
	}
}

// TestCtrlYNoCommandStaysOpen verifies Ctrl+Y is a no-op (no quit, no output)
// when there is no command to yank.
func TestCtrlYNoCommandStaysOpen(t *testing.T) {
	m := Model{
		sessionReady: true,
		cancel:       func() {},
		messages: []ChatMessage{
			{Role: "assistant", Content: "I can't help with that right now.\nTry rephrasing your request."},
		},
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	mn := next.(Model)
	if mn.quitting {
		t.Fatal("Ctrl+Y with no command should not quit")
	}
	if mn.Output() != "" {
		t.Fatalf("Output() = %q, want empty", mn.Output())
	}
}
