package mcp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func cuaResultFixture(t *testing.T, raw string) *sdkmcp.CallToolResult {
	t.Helper()
	var structured any
	if err := json.Unmarshal([]byte(raw), &structured); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return &sdkmcp.CallToolResult{StructuredContent: structured}
}

func boolPointer(value bool) *bool { return &value }

func TestCUAResultDecodesBindTabsWithoutGuessingActiveState(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []cuaTab
	}{
		{
			name: "one active tab",
			raw: `{
				"status":"ok","mode":"bind","target_id":"bt-one",
				"binding_quality":"exact","mutation_allowed":true,
				"tabs":[{"tab_id":"tab-one","title":"Inbox","url":"https://example.test/inbox","active":true}]
			}`,
			want: []cuaTab{{ID: "tab-one", Title: "Inbox", URL: "https://example.test/inbox", Active: boolPointer(true)}},
		},
		{
			name: "multiple false and unknown tabs",
			raw: `{
				"status":"ok","mode":"bind","target_id":"bt-many",
				"binding_quality":"exact","mutation_allowed":true,
				"tabs":[
					{"tab_id":"tab-a","title":"A","url":"https://a.test","active":false},
					{"tab_id":"tab-b","title":"B","url":"https://b.test","active":null},
					{"tab_id":"tab-c","title":"C","url":"https://c.test","active":true}
				]
			}`,
			want: []cuaTab{
				{ID: "tab-a", Title: "A", URL: "https://a.test", Active: boolPointer(false)},
				{ID: "tab-b", Title: "B", URL: "https://b.test", Active: nil},
				{ID: "tab-c", Title: "C", URL: "https://c.test", Active: boolPointer(true)},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got cuaResultEnvelope
			refusal, err := decodeCUAResult(cuaResultFixture(t, test.raw), &got)
			if err != nil {
				t.Fatalf("decodeCUAResult: %v", err)
			}
			if refusal != nil {
				t.Fatalf("unexpected refusal: %#v", refusal)
			}
			if got.Mode != "bind" || got.BindingQuality != "exact" || !got.MutationAllowed {
				t.Fatalf("bind metadata = %#v", got)
			}
			if !reflect.DeepEqual(got.Tabs, test.want) {
				t.Fatalf("tabs = %#v, want %#v", got.Tabs, test.want)
			}
		})
	}
}

func TestCUAResultDecodesSemanticSnapshotAndContentRefs(t *testing.T) {
	result := cuaResultFixture(t, `{
		"status":"ok","mode":"snapshot","target_id":"bt-semantic","tab_id":"tab-mail",
		"snapshot":{
			"id":"p42","format":"semantic_v2","complete":false,"scope":"viewport",
			"selected_nodes":300,"total_nodes":318,"node_budget":300,
			"omitted":{
				"css_hidden":410,"offscreen":82,"page_occluded":1,"no_layout":2,
				"unknown":3,"budget":18,"unprovable_frame":4
			},
			"continuation":"bc-next"
		},
		"page":{"url":"https://example.test/inbox","title":"Inbox"},
		"outline":"- heading \"Message\"\n- textbox \"Reply body\" p42:8",
		"refs":[{
			"ref":"p42:8","role":"textbox","name":"Reply body","value":"draft",
			"states":{"disabled":false,"focused":true},"actions":["type","click"],
			"frame":"main","visibility":"in_viewport"
		}],
		"content_refs":[{
			"ref":"p42:9","role":"heading","name":"Message","value":null,
			"states":{},"actions":[],"frame":"main","visibility":"near_viewport"
		}]
	}`)
	var got cuaResultEnvelope
	refusal, err := decodeCUAResult(result, &got)
	if err != nil || refusal != nil {
		t.Fatalf("decode = refusal %#v, error %v", refusal, err)
	}
	if got.TabID != "tab-mail" || got.Snapshot.ID != "p42" || got.Snapshot.Format != "semantic_v2" {
		t.Fatalf("snapshot identity = %#v", got)
	}
	if got.Snapshot.Complete || got.Snapshot.Continuation != "bc-next" {
		t.Fatalf("snapshot completion = %#v", got.Snapshot)
	}
	if got.Snapshot.SelectedNodes != 300 || got.Snapshot.TotalNodes != 318 || got.Snapshot.NodeBudget != 300 {
		t.Fatalf("snapshot counts = %#v", got.Snapshot)
	}
	if got.Snapshot.Omitted.total() != 520 {
		t.Fatalf("omission total = %d, want 520", got.Snapshot.Omitted.total())
	}
	if got.Page.URL != "https://example.test/inbox" || got.Page.Title != "Inbox" {
		t.Fatalf("page = %#v", got.Page)
	}
	if len(got.Refs) != 1 {
		t.Fatalf("action refs = %#v", got.Refs)
	}
	actionsMatch := reflect.DeepEqual(got.Refs[0].Actions, []string{"type", "click"})
	focused := got.Refs[0].States["focused"] == true
	if !actionsMatch || !focused {
		t.Fatalf("action refs = %#v", got.Refs)
	}
	if len(got.ContentRefs) != 1 || got.ContentRefs[0].Ref != "p42:9" || got.ContentRefs[0].Value != "" {
		t.Fatalf("content refs = %#v", got.ContentRefs)
	}
}

func TestCUAResultDecodesScreenshotAndLaterResultFields(t *testing.T) {
	result := cuaResultFixture(t, `{
		"status":"ok","prepared_pid":812,"pid":900,
		"windows":[{"pid":900,"window_id":77,"title":"Chromium","app":"Chromium"}],
		"dialog_id":"dialog-4","kind":"confirm","present":true,
		"download_id":"download-private","bytes":4096,"file_count":2,
		"screenshot":{
			"source":"cdp_tab","scope":"viewport","mime_type":"image/png",
			"width":1440,"height":900,"coordinate_space":"viewport_css_px",
			"viewport_css_width":960.5,"viewport_css_height":600.25,
			"pixel_to_css_scale_x":1.5,"pixel_to_css_scale_y":1.5,
			"tab_activation":"not_requested","window_foregrounding":"not_requested"
		}
	}`)
	var got cuaResultEnvelope
	refusal, err := decodeCUAResult(result, &got)
	if err != nil || refusal != nil {
		t.Fatalf("decode = refusal %#v, error %v", refusal, err)
	}
	if got.Status != "ok" || got.PreparedPID != 812 || got.PID != 900 || len(got.Windows) != 1 {
		t.Fatalf("result metadata = %#v", got)
	}
	dialogMatches := got.DialogID == "dialog-4" && got.Kind == "confirm" && got.Present
	downloadMatches := got.DownloadID == "download-private" && got.Bytes == 4096 && got.FileCount == 2
	if !dialogMatches || !downloadMatches {
		t.Fatalf("later fields = %#v", got)
	}
	screenshotMatches := got.Screenshot != nil && got.Screenshot.MIMEType == "image/png" &&
		got.Screenshot.ViewportCSSWidth == 960.5 && got.Screenshot.PixelToCSSScaleY == 1.5
	if !screenshotMatches {
		t.Fatalf("screenshot = %#v", got.Screenshot)
	}
}

func TestCUAResultKeepsImageContentSeparateFromScreenshotMetadata(t *testing.T) {
	result := cuaResultFixture(t, `{
		"status":"ok",
		"screenshot":{
			"source":"cdp_tab","scope":"viewport","mime_type":"image/png",
			"width":2,"height":1,"coordinate_space":"viewport_css_px",
			"viewport_css_width":1.0,"viewport_css_height":0.5,
			"pixel_to_css_scale_x":0.5,"pixel_to_css_scale_y":0.5,
			"tab_activation":"not_requested","window_foregrounding":"not_requested"
		}
	}`)
	result.Content = []sdkmcp.Content{
		&sdkmcp.ImageContent{MIMEType: "image/png", Data: []byte("literal-png-data")},
	}

	var got cuaResultEnvelope
	refusal, err := decodeCUAResult(result, &got)
	if err != nil || refusal != nil {
		t.Fatalf("decode = refusal %#v, error %v", refusal, err)
	}
	if got.Screenshot == nil || got.Screenshot.Width != 2 || got.Screenshot.ViewportCSSWidth != 1.0 {
		t.Fatalf("structured screenshot metadata = %#v", got.Screenshot)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content block count = %d, want 1", len(result.Content))
	}
	image, ok := result.Content[0].(*sdkmcp.ImageContent)
	if !ok {
		t.Fatalf("content type = %T, want *mcp.ImageContent", result.Content[0])
	}
	if image.MIMEType != "image/png" || string(image.Data) != "literal-png-data" {
		t.Fatalf("image content = %#v", image)
	}
}

func TestCUAResultDecodeLeavesContentValidationToTheConsumer(t *testing.T) {
	tests := []struct {
		name       string
		content    []sdkmcp.Content
		wantBlocks int
	}{
		{name: "empty content", content: []sdkmcp.Content{}, wantBlocks: 0},
		{
			name: "mixed text and image content",
			content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: "driver-owned text is not structured data"},
				&sdkmcp.ImageContent{MIMEType: "image/png", Data: []byte("png")},
			},
			wantBlocks: 2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := cuaResultFixture(t, `{
				"status":"ok",
				"screenshot":{
					"mime_type":"image/png","width":1,"height":1,
					"viewport_css_width":1,"viewport_css_height":1
				}
			}`)
			result.Content = test.content

			var got cuaResultEnvelope
			refusal, err := decodeCUAResult(result, &got)
			if err != nil || refusal != nil {
				t.Fatalf("decode = refusal %#v, error %v", refusal, err)
			}
			if got.Screenshot == nil || got.Screenshot.MIMEType != "image/png" {
				t.Fatalf("structured screenshot metadata = %#v", got.Screenshot)
			}
			if len(result.Content) != test.wantBlocks {
				t.Fatalf("content block count = %d, want %d", len(result.Content), test.wantBlocks)
			}
			if test.wantBlocks == 2 {
				text, textOK := result.Content[0].(*sdkmcp.TextContent)
				image, imageOK := result.Content[1].(*sdkmcp.ImageContent)
				if !textOK || text.Text != "driver-owned text is not structured data" {
					t.Fatalf("text content = %#v", result.Content[0])
				}
				if !imageOK || image.MIMEType != "image/png" || string(image.Data) != "png" {
					t.Fatalf("image content = %#v", result.Content[1])
				}
			}
		})
	}
}

func TestCUARefusalIsAFirstClassNonErrorResult(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantDetail any
	}{
		{
			name: "with detail",
			raw: `{
				"status":"refused",
				"refusal":{
					"code":"browser_ref_stale","message":"take a fresh snapshot",
					"detail":{"retryable":true}
				}
			}`,
			wantDetail: map[string]any{"retryable": true},
		},
		{
			name: "without detail",
			raw:  `{"status":"refused","refusal":{"code":"browser_requires_setup","message":"prepare the browser"}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var decoded cuaResultEnvelope
			refusal, err := decodeCUAResult(cuaResultFixture(t, test.raw), &decoded)
			if err != nil {
				t.Fatalf("structured refusal returned Go error: %v", err)
			}
			if refusal == nil || refusal.Code == "" || refusal.Message == "" {
				t.Fatalf("refusal = %#v", refusal)
			}
			if !reflect.DeepEqual(refusal.Detail, test.wantDetail) {
				t.Fatalf("detail = %#v, want %#v", refusal.Detail, test.wantDetail)
			}
		})
	}
}

func TestCUAResultRejectsTransportProtocolAndMalformedData(t *testing.T) {
	tests := []struct {
		name   string
		result *sdkmcp.CallToolResult
	}{
		{name: "nil result"},
		{
			name: "protocol error",
			result: &sdkmcp.CallToolResult{
				IsError: true,
				StructuredContent: map[string]any{
					"status":  "refused",
					"refusal": map[string]any{"code": "wrong", "message": "wrong"},
				},
			},
		},
		{name: "missing structured content", result: &sdkmcp.CallToolResult{}},
		{
			name: "unmarshalable structured content",
			result: &sdkmcp.CallToolResult{
				StructuredContent: map[string]any{"status": "ok", "bad": func() {}},
			},
		},
		{name: "structured content is not object", result: &sdkmcp.CallToolResult{StructuredContent: "not an object"}},
		{name: "missing status", result: cuaResultFixture(t, `{"mode":"bind"}`)},
		{name: "wrong status type", result: cuaResultFixture(t, `{"status":7}`)},
		{name: "partial status", result: cuaResultFixture(t, `{"status":"partial"}`)},
		{name: "completed status", result: cuaResultFixture(t, `{"status":"completed"}`)},
		{
			name: "wrong nested field type",
			result: cuaResultFixture(
				t,
				`{"status":"ok","refs":[{"ref":"p1:1","actions":"click"}]}`,
			),
		},
		{name: "refusal missing payload", result: cuaResultFixture(t, `{"status":"refused"}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var decoded cuaResultEnvelope
			refusal, err := decodeCUAResult(test.result, &decoded)
			if err == nil {
				t.Fatalf("decode returned refusal %#v without an error", refusal)
			}
			if refusal != nil {
				t.Fatalf("malformed/protocol result became refusal: %#v", refusal)
			}
		})
	}
}

func TestCUAResultUnsupportedStatusErrorDoesNotEchoBackendText(t *testing.T) {
	const secret = "partial raw-target raw-tab raw-ref backend-secret"
	var decoded cuaResultEnvelope
	refusal, err := decodeCUAResult(cuaResultFixture(t, `{"status":"`+secret+`"}`), &decoded)
	if err == nil || refusal != nil {
		t.Fatalf("decode returned refusal/error = %#v %v", refusal, err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "raw-target") || strings.Contains(err.Error(), "backend-secret") {
		t.Fatalf("unsupported status error echoed backend text: %v", err)
	}
}

func TestCUAAliasTargetChangeInvalidatesEveryCapabilityCache(t *testing.T) {
	state := newCUAAliasState()
	if changed := state.observeTarget("bt-old"); changed {
		t.Fatal("first observed target must not be reported as a generation change")
	}
	state.syncTabs([]cuaTab{{ID: "tab-old"}})
	state.selectedTab = "tab-old"
	state.refs = map[string]cuaElement{"@e1": {Raw: "p1:1", Actions: map[string]bool{"click": true}}}
	state.lastEditable = "p1:2"
	state.dialogID = "dialog-secret"

	if changed := state.observeTarget("bt-new"); !changed {
		t.Fatal("changed target was not reported")
	}
	aliasesRemain := len(state.tabs) != 0 || len(state.tabAliases) != 0 ||
		len(state.tabReverse) != 0 || len(state.refs) != 0
	if state.targetID != "bt-new" || aliasesRemain {
		t.Fatalf("alias caches survived target change: %#v", state)
	}
	if state.selectedTab != "" || state.lastEditable != "" || state.dialogID != "" || state.nextTabAlias != 0 {
		t.Fatalf("capability state survived target change: %#v", state)
	}
	state.syncTabs([]cuaTab{{ID: "tab-new"}})
	if state.tabReverse["tab-new"] != "@t1" {
		t.Fatalf("new generation alias = %q, want @t1", state.tabReverse["tab-new"])
	}
}

func TestCUAAliasTabsAreStableAndDeterministic(t *testing.T) {
	state := newCUAAliasState()
	state.observeTarget("bt-tabs")
	state.syncTabs([]cuaTab{{ID: "tab-a"}, {ID: "tab-b"}})
	if state.tabReverse["tab-a"] != "@t1" || state.tabReverse["tab-b"] != "@t2" {
		t.Fatalf("initial aliases = %#v", state.tabReverse)
	}
	state.syncTabs([]cuaTab{{ID: "tab-b"}, {ID: "tab-c"}})
	if _, stale := state.tabReverse["tab-a"]; stale {
		t.Fatalf("closed tab alias survived: %#v", state.tabReverse)
	}
	if state.tabReverse["tab-b"] != "@t2" || state.tabReverse["tab-c"] != "@t3" {
		t.Fatalf("refreshed aliases = %#v", state.tabReverse)
	}
}

func TestCUASnapshotRendersFreshAliasesAndHidesOpaqueCapabilities(t *testing.T) {
	state := newCUAAliasState()
	state.observeTarget("bt-private")
	state.syncTabs([]cuaTab{{ID: "tab-private"}})
	state.lastEditable = "old-editable"
	state.dialogID = "old-dialog"
	envelope := cuaResultEnvelope{
		Status:   "ok",
		TargetID: "bt-private",
		TabID:    "tab-private",
		Outline:  "- group bt-private tab-private bc-private\n  - button p1:1\n  - textbox p1:10",
		Snapshot: cuaSnapshotMeta{Complete: false, Continuation: "bc-private"},
		Refs: []cuaSemanticRef{
			{Ref: "p1:1", Role: "button", Name: "Send", Actions: []string{"click"}},
			{Ref: "p1:10", Role: "textbox", Name: "Body", Value: "draft", Actions: []string{"type", "click"}},
		},
		ContentRefs: []cuaSemanticRef{{Ref: "p1:99", Role: "heading", Name: "Private content capability"}},
	}
	output := renderCUASnapshot(state, envelope)
	if output.Status != "ok" || output.ElementCount != 2 {
		t.Fatalf("output = %#v", output)
	}
	for _, secret := range []string{"bt-private", "tab-private", "bc-private", "p1:1", "p1:10", "p1:99"} {
		if strings.Contains(output.Snapshot, secret) {
			t.Fatalf("snapshot leaked %q:\n%s", secret, output.Snapshot)
		}
	}
	visibleValues := []string{
		"@e1", "@e2", `button "Send" [click]`, `textbox "Body" value="draft" [type,click]`,
		"snapshot incomplete", "continuation available",
	}
	for _, visible := range visibleValues {
		if !strings.Contains(output.Snapshot, visible) {
			t.Fatalf("snapshot missing %q:\n%s", visible, output.Snapshot)
		}
	}
	firstRefMatches := state.refs["@e1"].Raw == "p1:1" && state.refs["@e1"].Actions["click"]
	secondRefMatches := state.refs["@e2"].Raw == "p1:10" && state.refs["@e2"].Actions["type"]
	if !firstRefMatches || !secondRefMatches {
		t.Fatalf("installed refs = %#v", state.refs)
	}
	if state.lastEditable != "" || state.dialogID != "" {
		t.Fatalf("fresh snapshot retained stale capabilities: %#v", state)
	}

	second := renderCUASnapshot(state, cuaResultEnvelope{
		Status: "ok", Snapshot: cuaSnapshotMeta{Complete: true},
		Refs: []cuaSemanticRef{{Ref: "p2:7", Role: "link", Name: "Next", Actions: []string{"click"}}},
	})
	if !strings.Contains(second.Snapshot, "@e1") || len(state.refs) != 1 || state.refs["@e1"].Raw != "p2:7" {
		t.Fatalf("fresh snapshot aliases = %#v, text %q", state.refs, second.Snapshot)
	}
}

func TestCUASnapshotReplacesPrefixCollidingRefsLongestFirst(t *testing.T) {
	state := newCUAAliasState()
	output := renderCUASnapshot(state, cuaResultEnvelope{
		Status:   "ok",
		Snapshot: cuaSnapshotMeta{Complete: true},
		Outline:  "- p1:10 before p1:1\n- p1:1 before p1:10",
		Refs: []cuaSemanticRef{
			{Ref: "p1:1", Role: "button", Name: "One", Actions: []string{"click"}},
			{Ref: "p1:10", Role: "button", Name: "Ten", Actions: []string{"click"}},
		},
	})
	if !strings.Contains(output.Snapshot, "- @e2 before @e1\n- @e1 before @e2") {
		t.Fatalf("prefix replacement corrupted aliases:\n%s", output.Snapshot)
	}
	if strings.Contains(output.Snapshot, "p1:") {
		t.Fatalf("raw refs survived:\n%s", output.Snapshot)
	}
}

func TestCUASnapshotInstallsOnlyAliasesPresentAfterLineBoundaryTruncation(t *testing.T) {
	state := newCUAAliasState()
	output := renderCUASnapshot(state, cuaResultEnvelope{
		Status:   "ok",
		Snapshot: cuaSnapshotMeta{Complete: true},
		Outline:  "- retained outline mentions p9:3\n",
		Refs: []cuaSemanticRef{
			{Ref: "p9:1", Role: "button", Name: strings.Repeat("a", maxSnapshotChars), Actions: []string{"click"}},
			{Ref: "p9:2", Role: "button", Name: "removed", Actions: []string{"click"}},
			{Ref: "p9:3", Role: "button", Name: "removed but named above", Actions: []string{"click"}},
		},
	})
	if len(output.Snapshot) > maxSnapshotChars {
		t.Fatalf("snapshot length = %d, cap = %d", len(output.Snapshot), maxSnapshotChars)
	}
	if !strings.HasSuffix(output.Snapshot, "\n") || !strings.Contains(output.Snapshot, "snapshot truncated at nib limit") {
		t.Fatalf("missing line-boundary truncation marker:\n%s", output.Snapshot)
	}
	if strings.Contains(output.Snapshot, strings.Repeat("a", 100)) {
		t.Fatalf("truncation retained a partial element line:\n%s", output.Snapshot)
	}
	if _, present := state.refs["@e1"]; present {
		t.Fatalf("truncated @e1 installed: %#v", state.refs)
	}
	if _, present := state.refs["@e2"]; present {
		t.Fatalf("truncated @e2 installed: %#v", state.refs)
	}
	if got := state.refs["@e3"].Raw; got != "p9:3" {
		t.Fatalf("outline-retained @e3 = %q, refs %#v", got, state.refs)
	}
}

func TestCUASnapshotMarksIncompleteOrOmittedResults(t *testing.T) {
	tests := []struct {
		name     string
		snapshot cuaSnapshotMeta
		want     []string
	}{
		{name: "incomplete", snapshot: cuaSnapshotMeta{Complete: false}, want: []string{"snapshot incomplete"}},
		{
			name:     "continuation",
			snapshot: cuaSnapshotMeta{Complete: true, Continuation: "bc-secret"},
			want:     []string{"snapshot incomplete", "continuation available"},
		},
		{
			name:     "omissions",
			snapshot: cuaSnapshotMeta{Complete: true, Omitted: cuaOmissionCounts{Budget: 7, Offscreen: 2}},
			want:     []string{"snapshot incomplete", "9 nodes omitted"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := renderCUASnapshot(
				newCUAAliasState(),
				cuaResultEnvelope{Status: "ok", Outline: "- heading", Snapshot: test.snapshot},
			)
			for _, want := range test.want {
				if !strings.Contains(output.Snapshot, want) {
					t.Fatalf("snapshot missing %q: %q", want, output.Snapshot)
				}
			}
			if strings.Contains(output.Snapshot, test.snapshot.Continuation) && test.snapshot.Continuation != "" {
				t.Fatalf("continuation leaked: %q", output.Snapshot)
			}
		})
	}
}

func TestCUARefusalPublicConversionRedactsCapabilitiesAndFilesystemValues(t *testing.T) {
	state := newCUAAliasState()
	state.observeTarget("bt-secret")
	state.syncTabs([]cuaTab{{ID: "tab-secret"}})
	state.refs = map[string]cuaElement{"@e4": {Raw: "p7:4", Actions: map[string]bool{"upload": true}}}
	refusal := &cuaRefusal{
		Code:    "browser_action_unavailable",
		Message: "target bt-secret tab tab-secret ref p7:4 rejected /private/in.txt under /private/downloads",
		Detail: map[string]any{
			"target_id":   "bt-secret",
			"safe_reason": "p7:4 in bt-secret for /private/in.txt",
			"nested": map[string]any{
				"endpoint": "ws://127.0.0.1/private",
				"filename": "in.txt",
				"safe":     "tab-secret and /private/downloads",
			},
			"items": []any{map[string]any{"continuation": "bc-secret"}, "p7:4"},
		},
	}
	public := state.publicRefusal(refusal, map[string]any{
		"files":            []any{"/private/in.txt"},
		"destination_root": "/private/downloads",
	})
	encoded, err := json.Marshal(public)
	if err != nil {
		t.Fatalf("marshal public refusal: %v", err)
	}
	text := string(encoded)
	secrets := []string{
		"bt-secret", "tab-secret", "p7:4", "/private/in.txt", "/private/downloads",
		"ws://127.0.0.1/private", "in.txt", "bc-secret",
	}
	for _, secret := range secrets {
		if strings.Contains(text, secret) {
			t.Fatalf("public refusal leaked %q: %s", secret, text)
		}
	}
	for _, replacement := range []string{"@t1", "@e4", "[redacted]"} {
		if !strings.Contains(text, replacement) {
			t.Fatalf("public refusal missing %q: %s", replacement, text)
		}
	}
	if public.Code != refusal.Code {
		t.Fatalf("code = %q, want %q", public.Code, refusal.Code)
	}
	detail, ok := public.Detail.(map[string]any)
	if !ok {
		t.Fatalf("detail type = %T", public.Detail)
	}
	if _, present := detail["target_id"]; present {
		t.Fatalf("sensitive detail key survived: %#v", detail)
	}
	nested, ok := detail["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested detail type = %T", detail["nested"])
	}
	if _, present := nested["endpoint"]; present {
		t.Fatalf("nested endpoint survived: %#v", nested)
	}
	if _, present := nested["filename"]; present {
		t.Fatalf("nested filename survived: %#v", nested)
	}
}

func TestCUARefusalRedactsOpaqueArgumentCapabilitiesFromMessageValuesAndKeys(t *testing.T) {
	secrets := map[string]string{
		"target_id":    "bt-argument-only",
		"tab_id":       "tab-argument-only",
		"ref":          "p88:4",
		"continuation": "bc-argument-only",
		"endpoint":     "ws://127.0.0.1:9555/devtools/browser/private",
		"dialog_id":    "dialog-argument-only",
		"download_id":  "download-argument-only",
	}
	refusal := &cuaRefusal{
		Code: "browser_ref_stale",
		Message: strings.Join([]string{
			secrets["target_id"], secrets["tab_id"], secrets["ref"], secrets["continuation"],
			secrets["endpoint"], secrets["dialog_id"], secrets["download_id"],
		}, " "),
		Detail: map[string]any{
			"safe_reason": strings.Join([]string{
				secrets["continuation"], secrets["target_id"], secrets["dialog_id"],
			}, " "),
			secrets["continuation"]: "opaque token was used as a map key",
			"nested": map[string]any{
				secrets["ref"]: secrets["tab_id"],
			},
		},
	}

	public := newCUAAliasState().publicRefusal(refusal, map[string]any{
		"target_id":    secrets["target_id"],
		"tab_id":       secrets["tab_id"],
		"ref":          secrets["ref"],
		"continuation": secrets["continuation"],
		"endpoint":     secrets["endpoint"],
		"dialog_id":    secrets["dialog_id"],
		"download_id":  secrets["download_id"],
	})
	encoded, err := json.Marshal(public)
	if err != nil {
		t.Fatalf("marshal public refusal: %v", err)
	}
	for name, secret := range secrets {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("public refusal leaked %s %q: %s", name, secret, encoded)
		}
	}
	if !strings.Contains(string(encoded), "[continuation]") {
		t.Fatalf("continuation was not replaced with a safe marker: %s", encoded)
	}
}

func TestCUARefusalRedactsSensitiveValuesFromConcreteJSONContainers(t *testing.T) {
	type fileArguments []map[string][]string
	type continuationArguments [1]map[string]string

	const (
		fileSecret         = "/private/typed-container.txt"
		continuationSecret = "bc-typed-container"
	)
	refusal := &cuaRefusal{
		Code:    "browser_action_unavailable",
		Message: fileSecret + " " + continuationSecret,
		Detail: map[string]any{
			"safe_reason": fileSecret + " " + continuationSecret,
		},
	}

	public := newCUAAliasState().publicRefusal(refusal, map[string]any{
		"files": fileArguments{{"nested": {fileSecret}}},
		"continuation": continuationArguments{{
			"nested": continuationSecret,
		}},
	})
	encoded, err := json.Marshal(public)
	if err != nil {
		t.Fatalf("marshal public refusal: %v", err)
	}
	for _, secret := range []string{fileSecret, continuationSecret} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("public refusal leaked typed-container value %q: %s", secret, encoded)
		}
	}
	for _, marker := range []string{"[redacted]", "[continuation]"} {
		if !strings.Contains(string(encoded), marker) {
			t.Fatalf("public refusal missing %q: %s", marker, encoded)
		}
	}
}

func TestCUARefusalFailsClosedWhenSensitiveArgumentCannotBeNormalized(t *testing.T) {
	const secret = "/private/unserializable.txt"
	refusal := &cuaRefusal{
		Code:    "browser_action_unavailable",
		Message: "could not upload " + secret,
		Detail: map[string]any{
			"safe_reason": "rejected " + secret,
		},
	}

	public := newCUAAliasState().publicRefusal(refusal, map[string]any{
		"files": []any{secret, func() {}},
	})
	if public.Code != refusal.Code {
		t.Fatalf("code = %q, want %q", public.Code, refusal.Code)
	}
	if public.Message != "browser action refused" {
		t.Fatalf("message = %q, want generic fail-closed message", public.Message)
	}
	if public.Detail != nil {
		t.Fatalf("detail = %#v, want no detail after sanitization failure", public.Detail)
	}
	encoded, err := json.Marshal(public)
	if err != nil {
		t.Fatalf("marshal public refusal: %v", err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("public refusal leaked %q after sanitization failure: %s", secret, encoded)
	}
}

func TestCUARefusalLeavesOrdinaryArgumentValuesPublic(t *testing.T) {
	const message = "balanced compact buffered"
	refusal := &cuaRefusal{
		Code:    "browser_action_unavailable",
		Message: message,
		Detail: map[string]any{
			"safe_reason": message,
		},
	}

	public := newCUAAliasState().publicRefusal(refusal, map[string]any{
		"preferred_mode": "balanced",
		"table_style":    "compact",
		"curl_output":    "buffered",
	})
	if public.Message != message {
		t.Fatalf("message = %q, want ordinary argument values unchanged", public.Message)
	}
	detail, ok := public.Detail.(map[string]any)
	if !ok {
		t.Fatalf("detail type = %T", public.Detail)
	}
	if detail["safe_reason"] != message {
		t.Fatalf("safe_reason = %#v, want %q", detail["safe_reason"], message)
	}
}

func TestCUARefusalDuplicateRawRefUsesEarliestAliasDeterministically(t *testing.T) {
	state := newCUAAliasState()
	state.refs = map[string]cuaElement{
		"@e10": {Raw: "p7:duplicate"},
		"@e2":  {Raw: "p7:duplicate"},
	}
	refusal := &cuaRefusal{
		Code:    "browser_ref_stale",
		Message: "p7:duplicate",
	}

	for iteration := 0; iteration < 128; iteration++ {
		public := state.publicRefusal(refusal, nil)
		if public.Message != "@e2" {
			t.Fatalf("iteration %d: message = %q, want earliest alias @e2", iteration, public.Message)
		}
	}
}
