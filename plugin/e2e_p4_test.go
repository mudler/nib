package plugin

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

func TestEndToEndClaudePlugin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	repo := gitInitRepoFiles(t, map[string]string{
		".claude-plugin/plugin.json": `{"name":"claudedemo","version":"0.1.0","description":"d"}`,
		"skills/helper/SKILL.md":     "---\nname: helper\ndescription: a claude skill\n---\nHelp body.\n",
		"hooks/hooks.json":           `{"hooks":{"PreToolUse":[{"matcher":"bash","hooks":[{"type":"command","command":"echo ok"}]}]}}`,
	})

	mgr := NewManager(base)
	m, err := mgr.Install(repo, "", "0.9.0")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if m.Name != "claudedemo" {
		t.Fatalf("claude plugin name wrong: %q", m.Name)
	}
	if err := mgr.SetEnabled("claudedemo", true); err != nil {
		t.Fatal(err)
	}

	cfg := types.Config{Prompt: "BASE"}
	if err := Apply(&cfg, base, "0.9.0"); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Skills) != 1 || cfg.Skills[0].Name != "helper" || cfg.Skills[0].Instructions != "Help body.\n" {
		t.Fatalf("claude skill not mapped: %+v", cfg.Skills)
	}
	if !strings.Contains(cfg.GetPrompt(), "helper: a claude skill") {
		t.Fatalf("claude skill not in prompt:\n%s", cfg.GetPrompt())
	}
	if len(cfg.Hooks) != 1 || cfg.Hooks[0].Event != "PreToolUse" || cfg.Hooks[0].Dir == "" {
		t.Fatalf("claude hook not mapped with Dir: %+v", cfg.Hooks)
	}
}
