package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mudler/wiz/types"
)

func gitInitRepo(t *testing.T, body string) string {
	t.Helper()
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, NativeManifestFile), body)
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "add", "."},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo
}

func TestEndToEndInstallEnableMerge(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	repo := gitInitRepo(t,
		"name: e2e\nversion: 0.1.0\n"+
			"mcp_servers:\n  e2emcp:\n    command: e2ecmd\n"+
			"agents:\n  - name: e2eagent\n    description: d\n")

	mgr := NewManager(base)
	m, err := mgr.Install(repo, "", "0.9.0")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if m.Name != "e2e" {
		t.Fatalf("name = %q", m.Name)
	}

	// Disabled by default → no contribution.
	cfg := &types.Config{}
	_ = Apply(cfg, base, "0.9.0")
	if len(cfg.Agents) != 0 || len(cfg.MCPServers) != 0 {
		t.Fatalf("disabled plugin contributed: %+v / %+v", cfg.Agents, cfg.MCPServers)
	}

	// Enable → contributes mcp + agent.
	if err := mgr.SetEnabled("e2e", true); err != nil {
		t.Fatal(err)
	}
	cfg = &types.Config{}
	if err := Apply(cfg, base, "0.9.0"); err != nil {
		t.Fatal(err)
	}
	if cfg.MCPServers["e2emcp"].Command != "e2ecmd" {
		t.Fatalf("mcp not merged: %+v", cfg.MCPServers)
	}
	if len(cfg.Agents) != 1 || cfg.Agents[0].Name != "e2eagent" {
		t.Fatalf("agent not merged: %+v", cfg.Agents)
	}

	// Clone really happened on disk.
	if _, err := os.Stat(filepath.Join(pluginDir(base, "e2e"), NativeManifestFile)); err != nil {
		t.Fatalf("plugin files missing: %v", err)
	}
}
