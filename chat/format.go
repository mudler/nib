package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// PrettyJSON indents a JSON string for display. If s is not valid JSON it is
// returned unchanged.
func PrettyJSON(s string) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(s), "", "  "); err != nil {
		return s
	}
	return buf.String()
}

// PreviewResult formats a tool result for compact display: it pretty-prints
// JSON (PrettyJSON), trims surrounding whitespace, then truncates to at most
// maxLines lines, appending a "… N more lines" note when it had to cut. Returns
// "" for empty/whitespace input. maxLines <= 0 means no line limit.
func PreviewResult(s string, maxLines int) string {
	s = strings.TrimRight(strings.TrimSpace(PrettyJSON(strings.TrimSpace(s))), "\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if maxLines > 0 && len(lines) > maxLines {
		extra := len(lines) - maxLines
		noun := "lines"
		if extra == 1 {
			noun = "line"
		}
		lines = lines[:maxLines]
		lines = append(lines, fmt.Sprintf("… %d more %s", extra, noun))
	}
	return strings.Join(lines, "\n")
}
