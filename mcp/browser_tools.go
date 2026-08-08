package mcp

import (
	"sort"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/xlog"
)

const (
	browserNavigateDescription = "Open a URL in a real, headed Chrome browser. ALWAYS call this first for any web task — it loads " +
		"the page and returns a snapshot of its interactive elements, each tagged with a ref like @e3. " +
		"For read-only lookups (answering a question, fetching a page's text) prefer web_search/web_fetch " +
		"instead — this tool is for tasks that require actually driving a page (clicking, typing, logging in)."
	browserSnapshotDescription = "Re-read the accessibility tree of the currently open page without navigating, returning fresh @eN " +
		"refs for browser_click/browser_type. Use after an action whose result isn't otherwise clear, or " +
		"when refs may be stale. Set full=true for the whole-page outline instead of just interactive elements."
	browserClickDescription = "Click the element identified by ref (an @eN id from the last browser_navigate/browser_snapshot/" +
		"browser_click/browser_type/browser_press/browser_scroll result). Returns a fresh snapshot."
	browserTypeDescription = "Focus the element identified by ref (an @eN id from the last snapshot), clear it, and type text into " +
		"it. Returns a fresh snapshot. Use browser_press with key=Enter afterward to submit, if needed."
	browserPressDescription = "Send a single named key (Enter, Tab, Escape, ArrowDown, PageUp, etc.) to whatever currently has focus " +
		"on the page — there is no ref. Returns a fresh snapshot. Useful to submit a form after browser_type."
	browserScrollDescription = "Scroll the page viewport up or down by about 90% of its height. direction must be \"up\" or \"down\". " +
		"Returns a fresh snapshot revealing the newly visible content."
	browserVisionDescription = "Take a screenshot of the current page for visual inspection — use when the accessibility snapshot " +
		"isn't enough (CAPTCHAs, visual verification, complex layouts). question says what to look for."
)

// BrowserInput is the shared input shape for every browser_* tool. Only the
// fields relevant to a given tool are populated by the model for that call.
type BrowserInput struct {
	URL       string `json:"url,omitempty" jsonschema:"the URL to open (browser_navigate)"`
	Ref       string `json:"ref,omitempty" jsonschema:"element ref from the snapshot, e.g. @e5 (browser_click/browser_type)"`
	Text      string `json:"text,omitempty" jsonschema:"text to type (browser_type)"`
	Key       string `json:"key,omitempty" jsonschema:"key to press, e.g. Enter, Tab, Escape (browser_press)"`
	Direction string `json:"direction,omitempty" jsonschema:"up or down (browser_scroll)"`
	Full      bool   `json:"full,omitempty" jsonschema:"return the full page snapshot, not just interactive elements (browser_snapshot)"`
	Question  string `json:"question,omitempty" jsonschema:"what to look for in the screenshot (browser_vision)"`
}

type BrowserRefusal struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  any    `json:"detail,omitempty"`
}

type BrowserOutcome struct {
	Status  string          `json:"status,omitempty"`
	Refusal *BrowserRefusal `json:"refusal,omitempty"`
}

// BrowserOutput is the structured result returned to the model for every
// browser_* tool that produces (or refreshes) a snapshot.
type BrowserOutput struct {
	BrowserOutcome
	Snapshot     string `json:"snapshot,omitempty"`
	ElementCount int    `json:"element_count,omitempty"`
}

type browserToolHandlers struct {
	navigate mcp.ToolHandlerFor[BrowserInput, BrowserOutput]
	snapshot mcp.ToolHandlerFor[BrowserInput, BrowserOutput]
	click    mcp.ToolHandlerFor[BrowserInput, BrowserOutput]
	typeText mcp.ToolHandlerFor[BrowserInput, BrowserOutput]
	press    mcp.ToolHandlerFor[BrowserInput, BrowserOutput]
	scroll   mcp.ToolHandlerFor[BrowserInput, BrowserOutput]
	vision   mcp.ToolHandlerFor[BrowserInput, BrowserOutput]
}

func registerBrowserTools(server *mcp.Server, handlers browserToolHandlers) {
	addBrowserTool(server, "browser_navigate", browserNavigateDescription, handlers.navigate)
	addBrowserTool(server, "browser_snapshot", browserSnapshotDescription, handlers.snapshot)
	addBrowserTool(server, "browser_click", browserClickDescription, handlers.click)
	addBrowserTool(server, "browser_type", browserTypeDescription, handlers.typeText)
	addBrowserTool(server, "browser_press", browserPressDescription, handlers.press)
	addBrowserTool(server, "browser_scroll", browserScrollDescription, handlers.scroll)
	addBrowserTool(server, "browser_vision", browserVisionDescription, handlers.vision)
}

func addBrowserTool(
	server *mcp.Server,
	name string,
	description string,
	handler mcp.ToolHandlerFor[BrowserInput, BrowserOutput],
) {
	tool := &mcp.Tool{Name: name, Description: description}
	// Build a fresh schema per tool registration. Sharing one schema pointer
	// risks the SDK or a caller mutating every registered tool through an alias.
	if schema, err := browserInputSchema(); err != nil {
		xlog.Warn("browser: could not build enum schema, falling back to inferred", "tool", name, "err", err)
	} else {
		tool.InputSchema = schema
	}
	mcp.AddTool(server, tool, handler)
}

func browserInputSchema() (*jsonschema.Schema, error) {
	schema, err := jsonschema.For[BrowserInput](nil)
	if err != nil {
		return nil, err
	}
	if property := schema.Properties["direction"]; property != nil {
		property.Enum = []any{"up", "down"}
	}
	if property := schema.Properties["key"]; property != nil {
		keys := make([]string, 0, len(pressAllowedKeys))
		for key := range pressAllowedKeys {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		enum := make([]any, len(keys))
		for index, key := range keys {
			enum[index] = key
		}
		property.Enum = enum
	}
	return schema, nil
}
