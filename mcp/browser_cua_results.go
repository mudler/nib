package mcp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type cuaRefusal struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  any    `json:"detail,omitempty"`
}

type cuaTab struct {
	ID     string `json:"tab_id"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Active *bool  `json:"active"`
}

type cuaSemanticRef struct {
	Ref        string         `json:"ref"`
	Role       string         `json:"role"`
	Name       string         `json:"name"`
	Value      string         `json:"value"`
	States     map[string]any `json:"states"`
	Actions    []string       `json:"actions"`
	Frame      string         `json:"frame"`
	Visibility string         `json:"visibility"`
}

type cuaOmissionCounts struct {
	CSSHidden       int `json:"css_hidden"`
	Offscreen       int `json:"offscreen"`
	PageOccluded    int `json:"page_occluded"`
	NoLayout        int `json:"no_layout"`
	Unknown         int `json:"unknown"`
	Budget          int `json:"budget"`
	UnprovableFrame int `json:"unprovable_frame"`
}

func (counts cuaOmissionCounts) total() int64 {
	return int64(counts.CSSHidden) + int64(counts.Offscreen) + int64(counts.PageOccluded) +
		int64(counts.NoLayout) + int64(counts.Unknown) + int64(counts.Budget) +
		int64(counts.UnprovableFrame)
}

type cuaSnapshotMeta struct {
	ID            string            `json:"id"`
	Format        string            `json:"format"`
	Complete      bool              `json:"complete"`
	Scope         string            `json:"scope"`
	SelectedNodes int               `json:"selected_nodes"`
	TotalNodes    int               `json:"total_nodes"`
	NodeBudget    int               `json:"node_budget"`
	Omitted       cuaOmissionCounts `json:"omitted"`
	Continuation  string            `json:"continuation"`
}

type cuaPage struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

type cuaScreenshot struct {
	Source              string  `json:"source"`
	Scope               string  `json:"scope"`
	MIMEType            string  `json:"mime_type"`
	Width               int     `json:"width"`
	Height              int     `json:"height"`
	CoordinateSpace     string  `json:"coordinate_space"`
	ViewportCSSWidth    float64 `json:"viewport_css_width"`
	ViewportCSSHeight   float64 `json:"viewport_css_height"`
	PixelToCSSScaleX    float64 `json:"pixel_to_css_scale_x"`
	PixelToCSSScaleY    float64 `json:"pixel_to_css_scale_y"`
	TabActivation       string  `json:"tab_activation"`
	WindowForegrounding string  `json:"window_foregrounding"`
}

type cuaWindow struct {
	PID        int    `json:"pid"`
	WindowID   int    `json:"window_id"`
	Title      string `json:"title"`
	App        string `json:"app"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	LaunchPath string `json:"launch_path"`
}

type cuaResultEnvelope struct {
	Status          string           `json:"status"`
	Mode            string           `json:"mode"`
	TargetID        string           `json:"target_id"`
	TabID           string           `json:"tab_id"`
	BindingQuality  string           `json:"binding_quality"`
	MutationAllowed bool             `json:"mutation_allowed"`
	Tabs            []cuaTab         `json:"tabs"`
	Snapshot        cuaSnapshotMeta  `json:"snapshot"`
	Page            cuaPage          `json:"page"`
	Outline         string           `json:"outline"`
	Refs            []cuaSemanticRef `json:"refs"`
	ContentRefs     []cuaSemanticRef `json:"content_refs"`
	Refusal         *cuaRefusal      `json:"refusal"`
	Screenshot      *cuaScreenshot   `json:"screenshot"`

	PreparedPID   int         `json:"prepared_pid"`
	PID           int         `json:"pid"`
	Windows       []cuaWindow `json:"windows"`
	DialogID      string      `json:"dialog_id"`
	Kind          string      `json:"kind"`
	Present       bool        `json:"present"`
	DownloadID    string      `json:"download_id"`
	Bytes         int64       `json:"bytes"`
	FileCount     int         `json:"file_count"`
	Assigned      int         `json:"assigned"`
	AssignedCount int         `json:"assigned_count"`
}

// decodeCUAResult distinguishes successful structured output, a structured
// Cua refusal, and transport/protocol data that nib cannot safely interpret.
// On success dst is populated. On refusal dst is untouched and the refusal is
// returned with a nil Go error.
func decodeCUAResult(result *sdkmcp.CallToolResult, dst any) (*cuaRefusal, error) {
	if result == nil {
		return nil, fmt.Errorf("cua returned no tool result")
	}
	if result.IsError {
		return nil, fmt.Errorf("cua tool call reported a protocol error")
	}
	if result.StructuredContent == nil {
		return nil, fmt.Errorf("cua tool result omitted structured content")
	}

	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return nil, fmt.Errorf("encode cua structured content: %w", err)
	}
	var header struct {
		Status  string      `json:"status"`
		Refusal *cuaRefusal `json:"refusal"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, fmt.Errorf("decode cua result status: %w", err)
	}
	if header.Status == "" {
		return nil, fmt.Errorf("cua structured content omitted status")
	}
	if header.Status == "refused" {
		if header.Refusal == nil || header.Refusal.Code == "" || header.Refusal.Message == "" {
			return nil, fmt.Errorf("cua refusal omitted code or message")
		}
		return header.Refusal, nil
	}
	if dst == nil {
		return nil, fmt.Errorf("decode cua structured content: nil destination")
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return nil, fmt.Errorf("decode cua structured content: %w", err)
	}
	return nil, nil
}

type cuaElement struct {
	Raw     string
	Actions map[string]bool
}

// cuaAliasState owns only opaque-capability translation. The Cua browser
// server adds lifecycle and synchronization around this state in later tasks.
type cuaAliasState struct {
	targetID     string
	tabs         map[string]cuaTab
	tabAliases   map[string]string
	tabReverse   map[string]string
	nextTabAlias int
	selectedTab  string
	refs         map[string]cuaElement
	lastEditable string
	dialogID     string
}

func newCUAAliasState() *cuaAliasState {
	state := &cuaAliasState{}
	state.clearCapabilities()
	return state
}

func (state *cuaAliasState) clearCapabilities() {
	state.tabs = make(map[string]cuaTab)
	state.tabAliases = make(map[string]string)
	state.tabReverse = make(map[string]string)
	state.nextTabAlias = 0
	state.selectedTab = ""
	state.refs = make(map[string]cuaElement)
	state.lastEditable = ""
	state.dialogID = ""
}

// observeTarget returns true only when a previously observed target changes.
// A target change is the public connection-generation boundary and therefore
// invalidates every capability minted under the older target.
func (state *cuaAliasState) observeTarget(targetID string) bool {
	if targetID == "" {
		return false
	}
	if state.targetID == "" {
		state.targetID = targetID
		return false
	}
	if state.targetID == targetID {
		return false
	}
	state.targetID = targetID
	state.clearCapabilities()
	return true
}

func (state *cuaAliasState) syncTabs(tabs []cuaTab) {
	if state.tabs == nil {
		state.clearCapabilities()
	}
	live := make(map[string]bool, len(tabs))
	for _, tab := range tabs {
		if tab.ID == "" {
			continue
		}
		live[tab.ID] = true
		state.tabs[tab.ID] = tab
		if _, exists := state.tabReverse[tab.ID]; exists {
			continue
		}
		state.nextTabAlias++
		alias := fmt.Sprintf("@t%d", state.nextTabAlias)
		state.tabReverse[tab.ID] = alias
		state.tabAliases[alias] = tab.ID
	}
	for raw, alias := range state.tabReverse {
		if live[raw] {
			continue
		}
		delete(state.tabs, raw)
		delete(state.tabReverse, raw)
		delete(state.tabAliases, alias)
		if state.selectedTab == raw {
			state.selectedTab = ""
		}
	}
}

func renderCUASnapshot(state *cuaAliasState, result cuaResultEnvelope) BrowserOutput {
	if state == nil {
		state = newCUAAliasState()
	}
	state.observeTarget(result.TargetID)
	if result.Tabs != nil {
		state.syncTabs(result.Tabs)
	}

	candidates := make(map[string]cuaElement, len(result.Refs))
	replacements := state.capabilityReplacements(result)
	var lines []string
	if outline := strings.TrimSpace(applyReplacements(result.Outline, replacements)); outline != "" {
		lines = append(lines, outline)
	}
	for index, ref := range result.Refs {
		alias := fmt.Sprintf("@e%d", index+1)
		actions := make(map[string]bool, len(ref.Actions))
		for _, action := range ref.Actions {
			actions[action] = true
		}
		candidates[alias] = cuaElement{Raw: ref.Ref, Actions: actions}

		role := applyReplacements(ref.Role, replacements)
		if role == "" {
			role = "element"
		}
		line := fmt.Sprintf("%s %s %q", alias, role, applyReplacements(ref.Name, replacements))
		if ref.Value != "" {
			line += fmt.Sprintf(" value=%q", applyReplacements(ref.Value, replacements))
		}
		if len(ref.Actions) != 0 {
			safeActions := make([]string, len(ref.Actions))
			for actionIndex, action := range ref.Actions {
				safeActions[actionIndex] = applyReplacements(action, replacements)
			}
			line += " [" + strings.Join(safeActions, ",") + "]"
		}
		lines = append(lines, line)
	}

	body := strings.Join(lines, "\n")
	if body != "" {
		body += "\n"
	}
	text := finishCUASnapshot(body, result.Snapshot)

	visible := make(map[string]bool)
	for _, alias := range cuaElementAliasPattern.FindAllString(text, -1) {
		visible[alias] = true
	}
	state.refs = make(map[string]cuaElement, len(visible))
	for alias := range visible {
		if element, exists := candidates[alias]; exists {
			state.refs[alias] = element
		}
	}
	state.lastEditable = ""
	state.dialogID = ""

	return BrowserOutput{
		BrowserOutcome: BrowserOutcome{Status: "ok"},
		Snapshot:       text,
		ElementCount:   len(result.Refs),
	}
}

var cuaElementAliasPattern = regexp.MustCompile(`@e\d+\b`)

func finishCUASnapshot(body string, snapshot cuaSnapshotMeta) string {
	marker := cuaSnapshotMarker(snapshot, false)
	text := body + marker
	if len(text) <= maxSnapshotChars {
		return text
	}

	marker = cuaSnapshotMarker(snapshot, true)
	available := maxSnapshotChars - len(marker)
	if available <= 0 {
		return marker[:maxSnapshotChars-1] + "\n"
	}
	if available > len(body) {
		available = len(body)
	}
	kept := body[:available]
	if newline := strings.LastIndexByte(kept, '\n'); newline >= 0 {
		kept = kept[:newline+1]
	} else {
		kept = ""
	}
	return kept + marker
}

func cuaSnapshotMarker(snapshot cuaSnapshotMeta, truncated bool) string {
	omitted := snapshot.Omitted.total()
	if !truncated && snapshot.Complete && snapshot.Continuation == "" && omitted == 0 {
		return ""
	}

	parts := make([]string, 0, 3)
	if truncated {
		parts = append(parts, "snapshot truncated at nib limit")
	} else {
		parts = append(parts, "snapshot incomplete")
	}
	if truncated && !snapshot.Complete {
		parts = append(parts, "Cua snapshot incomplete")
	}
	if snapshot.Continuation != "" {
		parts = append(parts, "continuation available")
	}
	if omitted != 0 {
		parts = append(parts, fmt.Sprintf("%d nodes omitted", omitted))
	}
	return "… [" + strings.Join(parts, "; ") + "]\n"
}

func (state *cuaAliasState) capabilityReplacements(result cuaResultEnvelope) map[string]string {
	replacements := make(map[string]string)
	if state.targetID != "" {
		replacements[state.targetID] = "[target]"
	}
	if result.TargetID != "" {
		replacements[result.TargetID] = "[target]"
	}
	for raw, alias := range state.tabReverse {
		replacements[raw] = alias
	}
	if result.TabID != "" {
		if alias := state.tabReverse[result.TabID]; alias != "" {
			replacements[result.TabID] = alias
		} else {
			replacements[result.TabID] = "[tab]"
		}
	}
	if result.Snapshot.Continuation != "" {
		replacements[result.Snapshot.Continuation] = "[continuation]"
	}
	for index, ref := range result.Refs {
		if ref.Ref != "" {
			replacements[ref.Ref] = fmt.Sprintf("@e%d", index+1)
		}
	}
	for _, ref := range result.ContentRefs {
		if ref.Ref != "" {
			replacements[ref.Ref] = "[content]"
		}
	}
	return replacements
}

func (state *cuaAliasState) publicRefusal(refusal *cuaRefusal, args map[string]any) *BrowserRefusal {
	if refusal == nil {
		return nil
	}
	replacements := state.refusalReplacements(args)

	public := &BrowserRefusal{
		Code:    refusal.Code,
		Message: applyReplacements(refusal.Message, replacements),
	}
	if refusal.Detail != nil {
		var jsonDetail any
		if encoded, err := json.Marshal(refusal.Detail); err == nil && json.Unmarshal(encoded, &jsonDetail) == nil {
			public.Detail = sanitizeRefusalDetail(jsonDetail, replacements)
		}
	}
	return public
}

func (state *cuaAliasState) refusalReplacements(args map[string]any) map[string]string {
	replacements := make(map[string]string)
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if replacement, sensitive := refusalArgumentReplacement(key); sensitive {
			collectSensitiveStrings(args[key], replacement, replacements)
		}
	}

	if state != nil {
		if state.targetID != "" {
			replacements[state.targetID] = "[target]"
		}
		for raw, alias := range state.tabReverse {
			replacements[raw] = alias
		}
		for alias, element := range state.refs {
			if element.Raw != "" {
				replacements[element.Raw] = alias
			}
		}
	}
	return replacements
}

func refusalArgumentReplacement(key string) (string, bool) {
	lowerKey := strings.ToLower(key)
	switch {
	case strings.Contains(lowerKey, "continuation"):
		return "[continuation]", true
	case strings.Contains(lowerKey, "target"):
		return "[target]", true
	case strings.Contains(lowerKey, "tab"):
		return "[tab]", true
	case strings.Contains(lowerKey, "ref"):
		return "[ref]", true
	case strings.Contains(lowerKey, "endpoint"):
		return "[endpoint]", true
	case strings.Contains(lowerKey, "dialog"):
		return "[dialog]", true
	case strings.Contains(lowerKey, "download_id"):
		return "[download]", true
	case strings.Contains(lowerKey, "session"):
		return "[session]", true
	case lowerKey == "files", strings.Contains(lowerKey, "destination_root"):
		return "[redacted]", true
	case strings.Contains(lowerKey, "path"), strings.Contains(lowerKey, "url"),
		strings.Contains(lowerKey, "filename"):
		return "[redacted]", true
	default:
		return "", false
	}
}

func collectSensitiveStrings(value any, replacement string, replacements map[string]string) {
	switch typed := value.(type) {
	case string:
		if typed != "" {
			replacements[typed] = replacement
		}
	case []string:
		for _, item := range typed {
			collectSensitiveStrings(item, replacement, replacements)
		}
	case []any:
		for _, item := range typed {
			collectSensitiveStrings(item, replacement, replacements)
		}
	case map[string]string:
		for _, item := range typed {
			collectSensitiveStrings(item, replacement, replacements)
		}
	case map[string]any:
		for _, item := range typed {
			collectSensitiveStrings(item, replacement, replacements)
		}
	}
}

var sensitiveRefusalDetailKeys = []string{
	"target", "tab", "ref", "continuation", "endpoint", "path", "url", "filename",
	"dialog", "download", "session",
}

func sanitizeRefusalDetail(value any, replacements map[string]string) any {
	switch typed := value.(type) {
	case map[string]any:
		safe := make(map[string]any, len(typed))
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			item := typed[key]
			lowerKey := strings.ToLower(key)
			sensitive := false
			for _, fragment := range sensitiveRefusalDetailKeys {
				if strings.Contains(lowerKey, fragment) {
					sensitive = true
					break
				}
			}
			if !sensitive {
				safeKey := applyReplacements(key, replacements)
				if _, collision := safe[safeKey]; !collision {
					safe[safeKey] = sanitizeRefusalDetail(item, replacements)
				}
			}
		}
		return safe
	case []any:
		safe := make([]any, len(typed))
		for index, item := range typed {
			safe[index] = sanitizeRefusalDetail(item, replacements)
		}
		return safe
	case string:
		return applyReplacements(typed, replacements)
	default:
		return typed
	}
}

func applyReplacements(value string, replacements map[string]string) string {
	keys := make([]string, 0, len(replacements))
	for raw := range replacements {
		if raw != "" {
			keys = append(keys, raw)
		}
	}
	sort.Slice(keys, func(left, right int) bool {
		if len(keys[left]) == len(keys[right]) {
			return keys[left] < keys[right]
		}
		return len(keys[left]) > len(keys[right])
	})
	pairs := make([]string, 0, len(keys)*2)
	for _, raw := range keys {
		pairs = append(pairs, raw, replacements[raw])
	}
	if len(pairs) == 0 {
		return value
	}
	return strings.NewReplacer(pairs...).Replace(value)
}
