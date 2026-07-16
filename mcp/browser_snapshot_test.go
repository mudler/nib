package mcp

import (
	"fmt"
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

// TestBuildSnapshotTruncatesDensePages covers the review finding that a
// dense page emits an unbounded outline that can blow a small local model's
// context: a synthetic node set well past maxSnapshotChars must come back
// truncated at a line boundary with a marker, while every ref assigned
// during the (untruncated) tree walk still resolves.
func TestBuildSnapshotTruncatesDensePages(t *testing.T) {
	root := axNode{NodeID: "root", Role: "WebArea", Name: "Dense Page", BackendID: 0}
	nodes := []axNode{root}
	const n = 500
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("n%d", i)
		root.ChildIDs = append(root.ChildIDs, id)
		nodes = append(nodes, axNode{
			NodeID:    id,
			Role:      "link",
			Name:      fmt.Sprintf("A reasonably long link label number %d to pad the outline past the cap", i),
			BackendID: int64(1000 + i),
		})
	}
	nodes[0] = root // root's ChildIDs were only populated after it was appended

	text, refs := buildSnapshot(nodes, true)

	if len(text) > maxSnapshotChars+300 { // slack for the marker line itself
		t.Fatalf("truncated snapshot is still too long: %d chars", len(text))
	}
	if !strings.Contains(text, "truncated") {
		t.Fatalf("expected a truncation marker in output, got tail: %q", text[max(0, len(text)-200):])
	}
	// Every interactive node got a ref during the walk, truncation or not.
	if len(refs) != n {
		t.Fatalf("expected all %d refs to be assigned despite truncation, got %d", n, len(refs))
	}
	// Every @ref actually printed in the (truncated) text must resolve.
	for _, tok := range refTokens(text) {
		if _, ok := refs[tok]; !ok {
			t.Fatalf("ref %s in text but not in map", tok)
		}
	}
}
