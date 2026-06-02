package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

// copyTree recursively copies src into dst (files + dirs), preserving structure.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copyTree: %v", err)
	}
}

func gitCommitDir(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "add", "."},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// TestAcceptanceExamplePlugin is the plugin-system acceptance gate: the example
// plugin installs and contributes ALL six types.
func TestAcceptanceExamplePlugin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	exampleSrc, err := filepath.Abs(filepath.Join("..", "examples", "wiz-plugin-demo"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(exampleSrc, "wiz-plugin.yaml")); err != nil {
		t.Fatalf("example plugin not found at %s: %v", exampleSrc, err)
	}

	repo := t.TempDir()
	copyTree(t, exampleSrc, repo)
	gitCommitDir(t, repo)

	base := t.TempDir()
	mgr := NewManager(base)
	m, err := mgr.Install(repo, "", "0.9.0")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if m.Name != "wiz-demo" {
		t.Fatalf("name = %q", m.Name)
	}
	if err := mgr.SetEnabled("wiz-demo", true); err != nil {
		t.Fatal(err)
	}

	cfg := types.Config{Prompt: "BASE"}
	if err := Apply(&cfg, base, "0.9.0"); err != nil {
		t.Fatal(err)
	}

	if _, ok := cfg.MCPServers["echo"]; !ok {
		t.Fatalf("mcp server not merged: %+v", cfg.MCPServers)
	}
	if !hasAgentNamed(cfg.Agents, "demo-researcher") {
		t.Fatalf("agent not merged: %+v", cfg.Agents)
	}
	frags := strings.Join(cfg.PromptFragments, "\n")
	if !strings.Contains(frags, "DEMO_FRAGMENT_INLINE") || !strings.Contains(frags, "DEMO_FRAGMENT_FILE") {
		t.Fatalf("fragments not merged: %+v", cfg.PromptFragments)
	}
	if len(cfg.Skills) != 1 || cfg.Skills[0].Name != "demo-skill" || !strings.Contains(cfg.Skills[0].Instructions, "DEMO_SKILL_BODY") {
		t.Fatalf("skill not merged with body: %+v", cfg.Skills)
	}
	prompt := cfg.GetPrompt()
	if !strings.Contains(prompt, "demo-skill: DEMO_SKILL_INDEX") || !strings.Contains(prompt, "DEMO_FRAGMENT_INLINE") {
		t.Fatalf("prompt missing skill index / fragment:\n%s", prompt)
	}
	if len(cfg.Commands) != 1 || cfg.Commands[0].Name != "demo-review" || cfg.Commands[0].Agent != "demo-researcher" {
		t.Fatalf("command not merged: %+v", cfg.Commands)
	}
	var sessionStart, preTool bool
	for _, h := range cfg.Hooks {
		if h.Dir == "" {
			t.Fatalf("hook Dir not stamped: %+v", h)
		}
		switch h.Event {
		case "SessionStart":
			sessionStart = true
		case "PreToolUse":
			preTool = true
		}
	}
	if !sessionStart || !preTool {
		t.Fatalf("hooks not merged (sessionStart=%v preTool=%v): %+v", sessionStart, preTool, cfg.Hooks)
	}
}

// TestAcceptanceClaudePlugin reaffirms a Claude-format plugin installs + maps.
func TestAcceptanceClaudePlugin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	repo := gitInitRepoFiles(t, map[string]string{
		".claude-plugin/plugin.json": `{"name":"claude-accept","version":"1.0.0","description":"d"}`,
		"skills/c/SKILL.md":          "---\nname: cskill\ndescription: claude skill\n---\nclaude body\n",
		"commands/cmd.md":            "---\ndescription: claude cmd\n---\nDo: $ARGUMENTS\n",
	})
	mgr := NewManager(base)
	if _, err := mgr.Install(repo, "", "0.9.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := mgr.SetEnabled("claude-accept", true); err != nil {
		t.Fatal(err)
	}
	cfg := types.Config{Prompt: "BASE"}
	if err := Apply(&cfg, base, "0.9.0"); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Skills) != 1 || cfg.Skills[0].Name != "cskill" {
		t.Fatalf("claude skill not mapped: %+v", cfg.Skills)
	}
	if len(cfg.Commands) != 1 || !strings.Contains(cfg.Commands[0].Prompt, "{{.Args}}") {
		t.Fatalf("claude command not mapped/translated: %+v", cfg.Commands)
	}
}

func hasAgentNamed(agents []types.AgentTypeConfig, name string) bool {
	for _, a := range agents {
		if a.Name == name {
			return true
		}
	}
	return false
}
