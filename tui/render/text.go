package render

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// TruncateLine caps a single line at w display cells, ending with an ellipsis.
// A non-positive budget returns the bare ellipsis rather than an unclamped line.
func TruncateLine(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	if w <= 0 {
		return "…"
	}
	if w == 1 {
		return "…"
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if used+rw > w-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "…"
}

// Wrap wraps text to fit within the specified width, preserving existing newlines
func Wrap(text string, width int) string {
	if width <= 0 {
		return text
	}

	var result strings.Builder
	lines := strings.Split(text, "\n")

	for _, line := range lines {
		if line == "" {
			result.WriteString("\n")
			continue
		}

		// Calculate the visual width (accounting for ANSI codes)
		visualWidth := lipgloss.Width(line)
		if visualWidth <= width {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		// Need to wrap this line
		words := strings.Fields(line)
		if len(words) == 0 {
			result.WriteString("\n")
			continue
		}

		currentLine := strings.Builder{}
		currentWidth := 0

		for i, word := range words {
			wordWidth := lipgloss.Width(word)

			// If a single word is longer than width, truncate it on a rune
			// boundary (byte slicing here would split a multibyte rune).
			if wordWidth > width && currentWidth == 0 {
				result.WriteString(TruncateRunes(word, width))
				result.WriteString("\n")
				continue
			}

			if currentWidth > 0 {
				// Check if adding this word would exceed width
				if currentWidth+1+wordWidth > width {
					// Write current line and start new one
					result.WriteString(currentLine.String())
					result.WriteString("\n")
					currentLine.Reset()
					currentWidth = 0
				} else {
					// Add space before word
					currentLine.WriteString(" ")
					currentWidth += 1
				}
			}

			currentLine.WriteString(word)
			currentWidth += wordWidth

			// If this is the last word, write the line
			if i == len(words)-1 {
				result.WriteString(currentLine.String())
				result.WriteString("\n")
			}
		}
	}

	return result.String()
}

// TruncateRunes shortens word to at most width display columns, breaking on a
// rune boundary and appending an ellipsis when there is room for it.
func TruncateRunes(word string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(word) <= width {
		return word
	}
	if width <= 1 {
		return "…"
	}
	var b strings.Builder
	used := 0
	for _, r := range word {
		rw := lipgloss.Width(string(r))
		if used+rw > width-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "…"
}

// ShortID truncates an id to a compact display form (an 8-character prefix).
// Shared by tui (tool/job labels) and every Presenter (agent-tagged tool
// labels), so the two never drift apart.
func ShortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// AppendCursor places a live cursor after the last visible cell, wrapping when full.
func AppendCursor(out, cursor string, width int) string {
	lines := strings.Split(out, "\n")
	i := len(lines) - 1
	for i > 0 && strings.TrimSpace(ansi.Strip(lines[i])) == "" {
		i--
	}
	visible := strings.TrimRight(ansi.Strip(lines[i]), " ")
	w := ansi.StringWidth(visible)
	line := ansi.Truncate(lines[i], w, "")
	if w+ansi.StringWidth(cursor) > width {
		lines[i] = line + "\n" + cursor
	} else {
		lines[i] = line + cursor
	}
	return strings.Join(lines, "\n")
}
