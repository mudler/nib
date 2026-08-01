package chat

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/nib/types"
)

// The session's configurator is what self-configuration and every subsequent
// reload go through. Under a root override it must be rooted at the injected
// directory, both for writes and for the reload's re-read: an embedded session
// that reloads from ~/.config/nib would pull in the user's real model.
func TestNewSessionConfiguratorHonorsBaseDir(t *testing.T) {
	t.Setenv("MODEL", "")
	t.Setenv("API_KEY", "")
	t.Setenv("BASE_URL", "")
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	userRoot := filepath.Join(xdg, "nib")
	if err := os.MkdirAll(userRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userRoot, "config.yaml"), []byte("model: user-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("model: embedded-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := NewSession(context.Background(), types.Config{Prompt: "hi", BaseDir: root}, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	eff, err := s.configurator.EffectiveConfig()
	if err != nil {
		t.Fatalf("EffectiveConfig: %v", err)
	}
	if eff.Model != "embedded-model" {
		t.Fatalf("reloaded Model = %q, want embedded-model", eff.Model)
	}
	if eff.BaseDir != root {
		t.Fatalf("reloaded BaseDir = %q, want %q", eff.BaseDir, root)
	}

	// A self-config write must land in the injected root, never in the user's.
	if err := s.configurator.AddMCPServer("demo", types.MCPServer{Command: "demo-mcp"}); err != nil {
		t.Fatalf("AddMCPServer: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(userRoot, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "model: user-model\n" {
		t.Fatalf("the user's real config was rewritten: %q", data)
	}
}
