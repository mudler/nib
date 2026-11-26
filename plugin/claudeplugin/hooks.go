package claudeplugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Hook handler types.
const (
	HookTypeCommand = "command"
	HookTypeHTTP    = "http"
	HookTypeMCPTool = "mcp_tool"
	HookTypePrompt  = "prompt"
	HookTypeAgent   = "agent"
)

// HookEvents lists the hook event names of the format. An event outside this
// list is still parsed, and reported in Plugin.Skipped.
var HookEvents = []string{
	"SessionStart", "Setup", "UserPromptSubmit", "UserPromptExpansion",
	"PreSkillShellCommand", "PreToolUse", "PermissionRequest", "PermissionDenied",
	"PostToolUse", "PostToolUseFailure", "PostToolBatch", "Notification",
	"MessageDisplay", "SubagentStart", "SubagentStop", "TaskCreated",
	"TaskCompleted", "Stop", "StopFailure", "TeammateIdle", "InstructionsLoaded",
	"ConfigChange", "CwdChanged", "DirectoryAdded", "FileChanged",
	"WorktreeCreate", "WorktreeRemove", "PreCompact", "PostCompact",
	"PreModelSwitch", "PostModelSwitch", "Elicitation", "ElicitationResult",
	"SessionEnd",
}

// noMatcherEvents ignore the matcher.
var noMatcherEvents = map[string]bool{
	"CwdChanged": true, "UserPromptSubmit": true, "PostToolBatch": true, "Stop": true,
	"TeammateIdle": true, "TaskCreated": true, "TaskCompleted": true,
	"WorktreeCreate": true, "WorktreeRemove": true, "MessageDisplay": true,
}

// toolEvents match their matcher against a tool name.
var toolEvents = map[string]bool{
	"PreToolUse": true, "PostToolUse": true, "PostToolUseFailure": true,
	"PermissionRequest": true, "PermissionDenied": true,
}

// KnownHookEvent reports whether name is a hook event of the format.
func KnownHookEvent(name string) bool { return slices.Contains(HookEvents, name) }

// IsToolEvent reports whether the event's matcher is matched against a tool
// name, so a host must translate tool names to match it.
func IsToolEvent(event string) bool { return toolEvents[event] }

// HookGroup is one matcher group of one event: the handlers that run when
// the event fires and the matcher matches.
type HookGroup struct {
	Event string
	// Matcher is as written. Use [MatchHook] to evaluate it. It is empty
	// for events that ignore matchers.
	Matcher  string
	Handlers []HookHandler
	// Source is the file the group came from, or "plugin.json" or
	// "marketplace entry" for inline hooks.
	Source string
}

// HookHandler is one hook handler, as data.
type HookHandler struct {
	Type          string `json:"type"`
	If            string `json:"if,omitempty"`
	Timeout       int    `json:"timeout,omitempty"` // seconds; 0 means the default
	StatusMessage string `json:"statusMessage,omitempty"`
	Once          bool   `json:"once,omitempty"`
	OnFailure     string `json:"onFailure,omitempty"` // "continue" or "block"

	// command
	Command     string   `json:"command,omitempty"`
	Args        []string `json:"args,omitempty"` // exec form when set
	Async       bool     `json:"async,omitempty"`
	AsyncRewake bool     `json:"asyncRewake,omitempty"`
	Shell       string   `json:"shell,omitempty"`

	// http
	URL            string            `json:"url,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	AllowedEnvVars []string          `json:"allowedEnvVars,omitempty"`

	// mcp_tool
	Server string         `json:"server,omitempty"`
	Tool   string         `json:"tool,omitempty"`
	Input  map[string]any `json:"input,omitempty"`

	// prompt, agent
	Prompt string `json:"prompt,omitempty"`
	Model  string `json:"model,omitempty"`
}

// TimeoutFor returns the handler's timeout for an event: its own Timeout when
// set, otherwise the format's default (600 s for command, http and mcp_tool,
// 30 s on UserPromptSubmit and model switches, 10 s on MessageDisplay; 30 s
// for prompt; 60 s for agent).
func (h HookHandler) TimeoutFor(event string) time.Duration {
	if h.Timeout > 0 {
		return time.Duration(h.Timeout) * time.Second
	}
	switch h.Type {
	case HookTypePrompt:
		return 30 * time.Second
	case HookTypeAgent:
		return 60 * time.Second
	}
	switch event {
	case "UserPromptSubmit", "PreModelSwitch", "PostModelSwitch":
		return 30 * time.Second
	case "MessageDisplay":
		return 10 * time.Second
	}
	return 600 * time.Second
}

var simpleMatcher = regexp.MustCompile(`^[A-Za-z0-9_\- ,|]*$`)

// MatchHook evaluates a matcher against a value (a tool name, a start
// source...) with the format's rules: "", "*" and an absent matcher match
// everything; a matcher of letters, digits, '_', '-', spaces, ',' and '|' is
// an exact name or a list split on '|' or ','; anything else is an
// unanchored regular expression. An invalid expression matches nothing.
func MatchHook(matcher, value string) bool {
	m := strings.TrimSpace(matcher)
	if m == "" || m == "*" {
		return true
	}
	if simpleMatcher.MatchString(m) {
		for _, alt := range SplitMatcher(m) {
			if alt == value {
				return true
			}
		}
		return false
	}
	re, err := regexp.Compile(m)
	return err == nil && re.MatchString(value)
}

// SplitMatcher returns the names of a simple matcher (see [MatchHook]), or
// nil when the matcher is a regular expression or matches everything.
func SplitMatcher(matcher string) []string {
	m := strings.TrimSpace(matcher)
	if m == "" || m == "*" || !simpleMatcher.MatchString(m) {
		return nil
	}
	var out []string
	for _, alt := range strings.FieldsFunc(m, func(r rune) bool { return r == '|' || r == ',' }) {
		if alt = strings.TrimSpace(alt); alt != "" {
			out = append(out, alt)
		}
	}
	return out
}

// hooksDoc is the shape of a hooks file.
type hooksDoc struct {
	Hooks   json.RawMessage `json:"hooks"`
	Modules json.RawMessage `json:"modules"`
}

type rawGroup struct {
	Matcher *string           `json:"matcher"`
	Hooks   []json.RawMessage `json:"hooks"`
}

// parseHookFile reads a hooks file ({"hooks": {Event: [...]}}).
func (l *loader) parseHookFile(path string) []HookGroup {
	data, err := os.ReadFile(path)
	if err != nil {
		l.p.skip(SkipReadError, "hook", l.rel(path), err.Error())
		return nil
	}
	var doc hooksDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		l.p.skip(SkipInvalidJSON, "hook", l.rel(path), "invalid JSON: "+err.Error())
		return nil
	}
	if len(doc.Modules) > 0 && string(doc.Modules) != "null" {
		l.p.skip(SkipUnsupportedComponent, "hook", l.rel(path), "JavaScript hook modules are not supported")
	}
	if len(doc.Hooks) == 0 || string(doc.Hooks) == "null" {
		return nil
	}
	return l.parseHookEvents(doc.Hooks, l.rel(path))
}

// parseHookEvents reads an event map ({Event: [group...]}).
func (l *loader) parseHookEvents(raw json.RawMessage, source string) []HookGroup {
	var events map[string][]json.RawMessage
	if err := json.Unmarshal(raw, &events); err != nil {
		l.p.skip(SkipInvalidJSON, "hook", source, "invalid hooks object: "+err.Error())
		return nil
	}
	var out []HookGroup
	for _, event := range slices.Sorted(maps.Keys(events)) {
		if !KnownHookEvent(event) {
			l.p.skip(SkipUnknownHookEvent, "hook", event, source+": unknown hook event")
		}
		for i, gr := range events[event] {
			var g rawGroup
			if err := json.Unmarshal(gr, &g); err != nil {
				l.p.skip(SkipInvalidHook, "hook", fmt.Sprintf("%s[%d]", event, i), source+": "+err.Error())
				continue
			}
			if g.Hooks == nil {
				l.p.skip(SkipInvalidHook, "hook", fmt.Sprintf("%s[%d]", event, i), source+": matcher group has no \"hooks\" list")
				continue
			}
			group := HookGroup{Event: event, Source: source}
			if g.Matcher != nil && !noMatcherEvents[event] {
				group.Matcher = *g.Matcher
			}
			for j, hr := range g.Hooks {
				h, err := parseHookHandler(hr)
				if err != nil {
					l.p.skip(hookErrCode(err), "hook", fmt.Sprintf("%s[%d].hooks[%d]", event, i, j), source+": "+err.Error())
					continue
				}
				group.Handlers = append(group.Handlers, h)
			}
			if len(group.Handlers) > 0 {
				out = append(out, group)
			}
		}
	}
	return out
}

var errUnknownHookType = errors.New("unknown hook type")

func hookErrCode(err error) SkipCode {
	if errors.Is(err, errUnknownHookType) {
		return SkipUnsupportedHookType
	}
	return SkipInvalidHook
}

func parseHookHandler(raw json.RawMessage) (HookHandler, error) {
	var h HookHandler
	if err := json.Unmarshal(raw, &h); err != nil {
		return HookHandler{}, err
	}
	if h.Type == "" && h.Command != "" {
		h.Type = HookTypeCommand
	}
	switch h.Type {
	case HookTypeCommand:
		if strings.TrimSpace(h.Command) == "" {
			return HookHandler{}, fmt.Errorf("command hook without command")
		}
	case HookTypeHTTP:
		if h.URL == "" {
			return HookHandler{}, fmt.Errorf("http hook without url")
		}
	case HookTypeMCPTool:
		if h.Server == "" || h.Tool == "" {
			return HookHandler{}, fmt.Errorf("mcp_tool hook needs server and tool")
		}
	case HookTypePrompt, HookTypeAgent:
		if h.Prompt == "" {
			return HookHandler{}, fmt.Errorf("%s hook without prompt", h.Type)
		}
	default:
		return HookHandler{}, fmt.Errorf("%w %q", errUnknownHookType, h.Type)
	}
	return h, nil
}

func (l *loader) loadHooks(m *manifest) {
	var groups []HookGroup
	if f := filepath.Join(l.p.Root, "hooks", "hooks.json"); isFile(f) {
		groups = append(groups, l.parseHookFile(f)...)
	}
	l.forEachShape("hook", m.Hooks, func(rel string) {
		abs, err := CheckPath(l.p.Root, rel, false)
		if err != nil {
			l.p.skip(SkipInvalidPath, "hook", rel, err.Error())
			return
		}
		if abs == filepath.Join(l.p.Root, "hooks", "hooks.json") {
			return // already loaded
		}
		groups = append(groups, l.parseHookFile(abs)...)
	}, func(inline json.RawMessage) {
		groups = append(groups, l.parseHookEvents(inline, "plugin.json")...)
	})
	if len(l.entryHooks) > 0 {
		var entry []HookGroup
		l.forEachShape("hook", l.entryHooks, func(rel string) {
			abs, err := CheckPath(l.p.Root, rel, false)
			if err != nil {
				l.p.skip(SkipInvalidPath, "hook", rel, err.Error())
				return
			}
			entry = append(entry, l.parseHookFile(abs)...)
		}, func(inline json.RawMessage) {
			entry = append(entry, l.parseHookEvents(inline, "marketplace entry")...)
		})
		replaced := map[string]bool{}
		for _, g := range entry {
			replaced[g.Event] = true
		}
		kept := groups[:0]
		for _, g := range groups {
			if !replaced[g.Event] {
				kept = append(kept, g)
			}
		}
		groups = append(kept, entry...)
	}
	l.p.Hooks = groups
}
