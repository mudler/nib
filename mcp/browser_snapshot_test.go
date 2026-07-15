package mcp

import (
	"strings"
	"testing"
)

func TestBuildSnapshotAssignsRefsToInteractiveNodes(t *testing.T) {
	// A tiny tree: WebArea > (heading, link, textbox, ignored-generic).
	nodes := []axNode{
		{NodeID: "1", Role: "WebArea", Name: "IMDb", BackendID: 100, ChildIDs: []string{"2", "3", "4", "5"}},
		{NodeID: "2", Role: "heading", Name: "Top Movies", BackendID: 101},
		{NodeID: "3", Role: "link", Name: "Home", BackendID: 102},
		{NodeID: "4", Role: "textbox", Name: "Search IMDb", BackendID: 103},
		{NodeID: "5", Role: "generic", Name: "", BackendID: 104, Ignored: true},
	}
	text, refs := buildSnapshot(nodes, true)
	// Interactive nodes (link, textbox) get refs; heading is context-only (shown
	// in the outline for readability, per contextRoles) and the ignored generic
	// gets neither.
	if refs["@e1"] != 102 && refs["@e1"] != 103 {
		t.Fatalf("expected refs to map to backend ids, got %+v", refs)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs (link, textbox), got %d: %v", len(refs), refs)
	}
	// The textbox line must carry its @ref and role+name.
	if !strings.Contains(text, "textbox") || !strings.Contains(text, "Search IMDb") {
		t.Fatalf("snapshot missing the textbox: %s", text)
	}
	if !strings.Contains(text, "@e") {
		t.Fatalf("snapshot must show @eN refs: %s", text)
	}
	// Every @ref in text must exist in the map.
	for _, tok := range refTokens(text) {
		if _, ok := refs[tok]; !ok {
			t.Fatalf("ref %s in text but not in map", tok)
		}
	}
}

func TestBuildSnapshotSkipsChromeAndEmptyGenerics(t *testing.T) {
	nodes := []axNode{
		{NodeID: "1", Role: "generic", Name: "", BackendID: 1, ChildIDs: []string{"2"}},
		{NodeID: "2", Role: "StaticText", Name: "hello", BackendID: 2}, // text, not interactive
	}
	_, refs := buildSnapshot(nodes, true)
	if len(refs) != 0 {
		t.Fatalf("non-interactive text/generic must not get refs, got %v", refs)
	}
}
