package mcp

import (
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

func TestResolveRefUnknownErrorsWithFreshSnapshotHint(t *testing.T) {
	b := newBrowserServer(types.BrowserConfig{})
	_, err := b.resolveRef("@e5")
	if err == nil {
		t.Fatal("expected error for unknown ref, got nil")
	}
	if !strings.Contains(err.Error(), "browser_snapshot") {
		t.Fatalf("error should point the model at a fresh browser_snapshot, got: %v", err)
	}
}

func TestResolveRefTrimsWhitespaceAndFindsKnownRef(t *testing.T) {
	b := newBrowserServer(types.BrowserConfig{})
	b.refs["@e5"] = 42

	id, err := b.resolveRef(" @e5 ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 42 {
		t.Fatalf("id = %d, want 42", id)
	}
}

func TestLiveRefErrorsWhenNoBrowserOpen(t *testing.T) {
	b := newBrowserServer(types.BrowserConfig{})
	_, _, err := b.liveRef("@e5")
	if err == nil {
		t.Fatal("expected error when no page is open, got nil")
	}
	if !strings.Contains(err.Error(), "browser_navigate") {
		t.Fatalf("error should point the model at browser_navigate, got: %v", err)
	}
}
