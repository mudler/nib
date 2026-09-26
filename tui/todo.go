package tui

import (
	"fmt"
	"strings"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/theme"
)

// renderTodoPanel renders the full todo list as a body-replacing panel
// (toggled with Ctrl+T). Mirrors the log viewer approach: the panel owns
// the body area entirely and suppresses the composer + footer.
func (m Model) renderTodoPanel() string {
	var b strings.Builder

	b.WriteString(theme.Brand.Render("todos"))
	b.WriteString("\n\n")

	if m.session == nil || m.session.TodoList() == nil {
		b.WriteString(theme.Help.Render("No todo list in this session."))
		return b.String()
	}

	items := m.session.TodoList().Items()
	if len(items) == 0 {
		b.WriteString(theme.Help.Render("The todo list is empty."))
		return b.String()
	}

	done, total := m.session.TodoList().Counts()
	b.WriteString(theme.Meta.Render(fmt.Sprintf("%d/%d completed", done, total)))
	b.WriteString("\n\n")

	w := m.width
	if w < 20 {
		w = 80
	}

	for i, it := range items {
		glyph := todoStatusGlyph(it.Status)
		label := it.Content

		var line string
		switch it.Status {
		case chat.TodoCompleted:
			line = theme.Done.Render(glyph) + " " + theme.Subtle.Render(label)
		case chat.TodoActive:
			line = theme.Running.Render(glyph) + " " + theme.Brand.Render(label)
		case chat.TodoCancelled:
			line = theme.Error.Render(glyph) + " " + theme.Subtle.Render(label)
		default:
			line = theme.Help.Render(glyph) + " " + label
		}

		b.WriteString(line)

		if it.ActiveForm != "" && it.Status == chat.TodoActive {
			b.WriteString(theme.Meta.Render(" — " + it.ActiveForm))
		}

		// Wrap long content to the panel width.
		if i < len(items)-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}

// todoStatusGlyph returns the display glyph for a todo status.
func todoStatusGlyph(s chat.TodoStatus) string {
	switch s {
	case chat.TodoCompleted:
		return "✓"
	case chat.TodoActive:
		return "◐"
	case chat.TodoCancelled:
		return "~"
	default:
		return "○"
	}
}
