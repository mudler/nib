package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
)

// toolMessage builds the transcript entry for a finished root-agent tool call.
// A write or edit that changed its file shows the diff; a read collapses to a
// line count (the file is for the model, not the reader); a write with no diff
// to show gets a bare header rather than its "success true" envelope; anything
// else shows its previewed output. The header is marked with the outcome.
func toolMessage(res chat.ToolResult, elapsed time.Duration) ChatMessage {
	msg := ChatMessage{Role: "tool", Name: res.Name, Arguments: res.Arguments, Status: render.ToolStatusOK, Images: res.Images}
	failed, detail := chat.ToolOutcome(res.Result)
	elapsedStr := ""
	if elapsed > 0 {
		elapsedStr = theme.Elapsed(elapsed)
	}
	metaWithElapsed := func(meta string) {
		if elapsedStr == "" {
			msg.Meta = meta
			return
		}
		if meta == "" {
			msg.Meta = elapsedStr
			return
		}
		msg.Meta = meta + " " + theme.Sep + " " + elapsedStr
	}
	if failed {
		msg.Status = render.ToolStatusFailed
		msg.Meta = detail
		// A built-in tool's failure is its "error" field; the rest of the
		// envelope (success false, replacements 0) says nothing more. Shell
		// output has no such field and shows its stdout/stderr instead, and a
		// call cogito could not run shows cogito's plain-text error as is.
		if e := resultError(res.Result); e != "" {
			msg.Content = e
		} else {
			msg.Content = chat.PreviewResult(res.Name, res.Result, toolOutputKeepLines)
		}
		metaWithElapsed(msg.Meta)
		return msg
	}
	if res.Change != nil {
		setChange(&msg, res.Change)
		if msg.Diff != nil {
			metaWithElapsed(msg.Meta)
			return msg
		}
	}
	switch res.Name {
	case "read":
		if n := readLineCount(res.Result); n > 0 {
			msg.Meta = fmt.Sprintf(theme.ToolLineCount, n)
		}
		metaWithElapsed(msg.Meta)
		return msg
	case "write":
		metaWithElapsed(msg.Meta)
		return msg
	}
	msg.Content = chat.PreviewResult(res.Name, res.Result, toolOutputKeepLines)
	metaWithElapsed(msg.Meta)
	return msg
}

// setChange puts c on msg as a diff with its "+N -M" meta, noting a new file.
// A change that alters nothing leaves msg without a diff.
func setChange(msg *ChatMessage, c *chat.FileChange) {
	d := c.Diff()
	if d.Empty() {
		return
	}
	msg.Diff = &d
	msg.Meta = changeMeta(c, render.DiffStat(d))
}

// changeMeta prefixes stat with "new file" when c created its file.
func changeMeta(c *chat.FileChange, stat string) string {
	if c.Created {
		return theme.DiffNewFile + " " + theme.Sep + " " + stat
	}
	return stat
}

// readLineCount returns how many lines a read result returned, from its
// content (a ranged read returns fewer than total_lines).
func readLineCount(result string) int {
	var r struct {
		Content string `json:"content"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(result)), &r) != nil || r.Content == "" {
		return 0
	}
	return strings.Count(strings.TrimRight(r.Content, "\n"), "\n") + 1
}

// resultError returns a JSON tool result's "error" field, or "".
func resultError(result string) string {
	var r struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(result)), &r) != nil {
		return ""
	}
	return strings.TrimSpace(r.Error)
}
