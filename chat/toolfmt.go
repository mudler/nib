package chat

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mudler/nib/theme"
)

// FormatToolCall renders a tool call's arguments as a compact, human-readable
// summary for display, replacing raw JSON. argsJSON is the marshaled arguments
// object. Known tools get a purpose-built one-liner (see toolFormatters); any
// other tool (MCP servers, plugins) falls back to humanized key: value lines.
// If argsJSON is not a JSON object the input is returned unchanged.
func FormatToolCall(name, argsJSON string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return argsJSON
	}
	if f, ok := toolFormatters[name]; ok {
		if out := f(args); out != "" {
			return out
		}
	}
	return humanizeArgs(args)
}

// toolFormatters maps a tool name to a formatter over its decoded arguments.
var toolFormatters = map[string]func(map[string]any) string{
	"bash":             fmtBash,
	"bash_background":  fmtBashBackground,
	"bash_jobs":        func(map[string]any) string { return "list shell jobs" },
	"bash_job_output":  func(a map[string]any) string { return "job output " + argStr(a, "job_id") },
	"bash_job_kill":    func(a map[string]any) string { return "kill job " + argStr(a, "job_id") },
	"read":             fmtRead,
	"write":            func(a map[string]any) string { return "write " + argStr(a, "path") },
	"edit":             fmtEdit,
	"glob":             func(a map[string]any) string { return "glob " + argStr(a, "pat") + " in " + argStrOr(a, "path", ".") },
	"grep":             func(a map[string]any) string { return "grep /" + argStr(a, "pat") + "/ in " + argStrOr(a, "path", ".") },
	"load_skill":       func(a map[string]any) string { return "load skill " + argStr(a, "name") },
	"ask_user":         func(a map[string]any) string { return "ask: " + argStr(a, "question") },
	"agent_logs":       func(a map[string]any) string { return "agent logs " + argStr(a, "agent_id") },
	"schedule_wakeup":  fmtWakeup,
	"cron":             fmtCron,
	"cron_list":        func(map[string]any) string { return "list cron jobs" },
	"cron_delete":      func(a map[string]any) string { return "cancel cron " + argStr(a, "id") },
	"spawn_agent":      func(a map[string]any) string { return "spawn " + argStr(a, "agent_type") + ": " + argStr(a, "task") },
	"check_agent":      func(a map[string]any) string { return "check agent " + argStr(a, "agent_id") },
	"get_agent_result": func(a map[string]any) string { return "result of agent " + argStr(a, "agent_id") },
}

func fmtBash(a map[string]any) string {
	s := "$ " + argStr(a, "script")
	if t := argStr(a, "timeout"); t != "" {
		s += "  (timeout " + t + "s)"
	}
	return s
}

func fmtBashBackground(a map[string]any) string {
	return "$ " + argStr(a, "script") + "  (background)"
}

func fmtRead(a map[string]any) string {
	s := "read " + argStr(a, "path")
	off, okOff := argInt(a, "offset")
	lim, okLim := argInt(a, "limit")
	if okOff && okLim {
		s += fmt.Sprintf("  (lines %d–%d)", off, off+lim)
	} else if okOff {
		s += fmt.Sprintf("  (from line %d)", off)
	}
	return s
}

func fmtEdit(a map[string]any) string {
	return "edit " + argStr(a, "path") + "\n  " + argStr(a, "old") + " " + theme.Arrow + " " + argStr(a, "new")
}

func fmtCron(a map[string]any) string {
	s := "cron " + argStr(a, "expr") + " " + theme.Arrow + " " + argStr(a, "prompt")
	if r, ok := a["recurring"].(bool); ok && !r {
		s += " (once)"
	}
	if d, ok := a["durable"].(bool); ok && d {
		s += " (durable)"
	}
	return s
}

func fmtWakeup(a map[string]any) string {
	s := "wake in " + argStr(a, "delay_seconds") + "s"
	// Args were renamed note→prompt (plus a new reason); note remains an input
	// back-compat alias. Prefer reason, then prompt, then note for the detail.
	if detail := argStrOr(a, "reason", argStrOr(a, "prompt", argStr(a, "note"))); detail != "" {
		s += " — " + detail
	}
	return s
}

// argStr returns args[key] rendered as a scalar string, or "" if absent.
func argStr(a map[string]any, key string) string {
	v, ok := a[key]
	if !ok {
		return ""
	}
	return stringifyArg(v)
}

// argStrOr is argStr with a default when the key is absent or empty.
func argStrOr(a map[string]any, key, def string) string {
	if s := argStr(a, key); s != "" {
		return s
	}
	return def
}

// argInt returns args[key] as an int (JSON numbers decode to float64).
func argInt(a map[string]any, key string) (int, bool) {
	v, ok := a[key]
	if !ok {
		return 0, false
	}
	f, ok := v.(float64)
	if !ok {
		return 0, false
	}
	return int(f), true
}

// humanizeArgs renders arbitrary arguments as sorted "key: value" lines. Scalar
// values render inline; multi-line strings render as an indented block
// beneath their key.
func humanizeArgs(args map[string]any) string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString("\n")
		}
		val := stringifyArg(args[k])
		if strings.Contains(val, "\n") {
			b.WriteString(k + ":\n" + indent(val, "  "))
		} else {
			b.WriteString(k + ": " + val)
		}
	}
	return b.String()
}

// stringifyArg renders a single decoded JSON value. Strings are returned as-is;
// everything else is rendered compactly via fmt. Whole-number floats (JSON
// numbers decode to float64) render without a trailing ".0".
func stringifyArg(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	case bool:
		return fmt.Sprintf("%t", t)
	case nil:
		return ""
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}

// indent prefixes every line of s with prefix.
func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}
