package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

func TestDiscoverChromePrefersExplicit(t *testing.T) {
	if got := discoverChrome("/my/chrome"); got != "/my/chrome" {
		t.Fatalf("explicit path must win, got %q", got)
	}
	// With no explicit path it returns "" or a real candidate — just must not panic.
	_ = discoverChrome("")
}

func TestProfileDirIsDedicatedAndStable(t *testing.T) {
	b := newBrowserServer(types.BrowserConfig{ProfileDir: "/tmp/x/browser-profile"})
	if b.profileDir() != "/tmp/x/browser-profile" {
		t.Fatalf("profileDir=%q", b.profileDir())
	}
}

func TestBrowserTypeRejectsEmptyText(t *testing.T) {
	b := newBrowserServer(types.BrowserConfig{})
	_, _, err := b.browserType(context.Background(), nil, BrowserInput{Ref: "@e5", Text: ""})
	if err == nil {
		t.Fatal("expected error for empty text, got nil")
	}
	if !strings.Contains(err.Error(), "browser_type") {
		t.Fatalf("error should name browser_type, got: %v", err)
	}
}

// TestBrowserPressRejectsUnknownKey checks the key allowlist is enforced
// before any browser is touched (keyForName is validated first), so this
// needs no live Chrome — an unopened browserServer is enough.
func TestBrowserPressRejectsUnknownKey(t *testing.T) {
	b := newBrowserServer(types.BrowserConfig{})
	_, _, err := b.browserPress(context.Background(), nil, BrowserInput{Key: "F13"})
	if err == nil {
		t.Fatal("expected error for a key outside the allowlist, got nil")
	}
	if !strings.Contains(err.Error(), "browser_press") {
		t.Fatalf("error should name browser_press, got: %v", err)
	}
	for _, want := range []string{"Enter", "Tab", "Escape", "ArrowUp", "PageDown"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should list valid keys (missing %q): %v", want, err)
		}
	}
}

// TestBrowserScrollRejectsUnknownDirection checks the direction allowlist is
// enforced before any browser is touched (scrollDelta is validated first),
// so this needs no live Chrome — an unopened browserServer is enough.
func TestBrowserScrollRejectsUnknownDirection(t *testing.T) {
	b := newBrowserServer(types.BrowserConfig{})
	_, _, err := b.browserScroll(context.Background(), nil, BrowserInput{Direction: "sideways"})
	if err == nil {
		t.Fatal("expected error for a direction outside {up,down}, got nil")
	}
	if !strings.Contains(err.Error(), "browser_scroll") {
		t.Fatalf("error should name browser_scroll, got: %v", err)
	}
	if !strings.Contains(err.Error(), "up") || !strings.Contains(err.Error(), "down") {
		t.Fatalf("error should list valid directions (up, down), got: %v", err)
	}
}

// TestBrowserVisionRejectsNoPageOpen checks the no-page-open guard is
// enforced before any browser is touched, mirroring the other browser_*
// handlers' liveCtx/liveRef checks — so this needs no live Chrome.
func TestBrowserVisionRejectsNoPageOpen(t *testing.T) {
	b := newBrowserServer(types.BrowserConfig{})
	_, _, err := b.browserVision(context.Background(), nil, BrowserInput{Question: "what's on screen?"})
	if err == nil {
		t.Fatal("expected error when no page is open, got nil")
	}
	if !strings.Contains(err.Error(), "no page open") {
		t.Fatalf("error should say no page is open, got: %v", err)
	}
}

// TestBrowserInputSchemaHasRealEnum mirrors
// TestComputerInputSchemaHasRealEnums: direction must carry a real JSON
// Schema enum {up, down} (not just prose in the description) so the engine
// can constrain it, and the per-field descriptions from the jsonschema
// struct tags must survive the stamping.
func TestBrowserInputSchemaHasRealEnum(t *testing.T) {
	s, err := browserInputSchema()
	if err != nil {
		t.Fatal(err)
	}
	dir := s.Properties["direction"]
	if dir == nil || len(dir.Enum) == 0 {
		t.Fatal("direction must carry a real JSON Schema enum, not just a description")
	}
	has := func(val string) bool {
		for _, e := range dir.Enum {
			if e == val {
				return true
			}
		}
		return false
	}
	if !has("up") || !has("down") {
		t.Fatalf("direction enum should be {up, down}, got: %v", dir.Enum)
	}
	if len(dir.Enum) != 2 {
		t.Fatalf("direction enum should only be {up, down}, got: %v", dir.Enum)
	}
	// Descriptions from the struct tags must survive the enum stamping.
	for _, prop := range []string{"url", "ref", "text", "key", "direction", "full", "question"} {
		p := s.Properties[prop]
		if p == nil {
			t.Fatalf("schema missing property %q", prop)
		}
		if p.Description == "" {
			t.Fatalf("property %q lost its description", prop)
		}
	}
}
