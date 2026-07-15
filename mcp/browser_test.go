package mcp

import (
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
