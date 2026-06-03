package chat

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
// Populated by per-tool functions; empty for now (fallback handles everything).
var toolFormatters = map[string]func(map[string]any) string{}

// humanizeArgs renders arbitrary arguments as sorted "key: value" lines. Scalar
// values render inline; multi-line or long strings render as an indented block
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
