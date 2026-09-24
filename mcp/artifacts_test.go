package mcp

import (
	"strings"
	"testing"
)

func TestArtifactStore_Search(t *testing.T) {
	store := NewArtifactStore()

	// Artifact 1: multi-line content with "foo" and "bar"
	uri1 := store.Save("compaction", "line one\nline two foo\nline three\nline four\nline five bar\nline six\nline seven")
	if uri1 == "" {
		t.Fatal("Save returned empty URI")
	}

	// Artifact 2: multi-line content with "foo"
	uri2 := store.Save("compaction", "alpha\nbeta foo\ngamma\ndelta")
	if uri2 == "" {
		t.Fatal("Save returned empty URI")
	}

	// Search with OR pattern — should match "foo" and "bar"
	results, err := store.Search("foo|bar")
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 matches (foo x2, bar x1), got %d", len(results))
	}

	// Verify results are sorted by artifact ID then line number
	// Artifact 1 (ID=1): "foo" on line 2, "bar" on line 5
	// Artifact 2 (ID=2): "foo" on line 2
	if results[0].ID != 1 || results[0].Line != 2 {
		t.Errorf("result[0]: expected ID=1 Line=2, got ID=%d Line=%d", results[0].ID, results[0].Line)
	}
	if results[1].ID != 1 || results[1].Line != 5 {
		t.Errorf("result[1]: expected ID=1 Line=5, got ID=%d Line=%d", results[1].ID, results[1].Line)
	}
	if results[2].ID != 2 || results[2].Line != 2 {
		t.Errorf("result[2]: expected ID=2 Line=2, got ID=%d Line=%d", results[2].ID, results[2].Line)
	}

	// Verify context lines are rendered with | and matching line with >
	text := results[0].Text
	if !strings.Contains(text, "2> ") {
		t.Errorf("expected matching line marked with '>', got:\n%s", text)
	}
	if !strings.Contains(text, "1| ") {
		t.Errorf("expected context line marked with '|', got:\n%s", text)
	}
	if !strings.Contains(text, "3| ") {
		t.Errorf("expected context line marked with '|', got:\n%s", text)
	}

	// Case-insensitive search: "FoO" should match "foo"
	results, err = store.Search("FoO")
	if err != nil {
		t.Fatalf("Search(FoO) returned error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 case-insensitive matches, got %d", len(results))
	}

	// Invalid regex should return error
	_, err = store.Search("[")
	if err == nil {
		t.Fatal("expected error for invalid regex '['")
	}

	// No matches
	results, err = store.Search("nomatchpattern")
	if err != nil {
		t.Fatalf("Search(nomatchpattern) returned error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 matches, got %d", len(results))
	}
}

func TestArtifactStore_SearchCap(t *testing.T) {
	store := NewArtifactStore()

	// Create content with 60 matching lines
	var b strings.Builder
	for i := 0; i < 60; i++ {
		b.WriteString("match line\n")
	}
	store.Save("compaction", b.String())

	results, err := store.Search("match")
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != 50 {
		t.Fatalf("expected 50 results (capped), got %d", len(results))
	}
}

func TestArtifactStore_SearchEmptyStore(t *testing.T) {
	store := NewArtifactStore()
	results, err := store.Search("anything")
	if err != nil {
		t.Fatalf("Search on empty store returned error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results from empty store, got %d", len(results))
	}
}
