package plugin

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

func gitInitRepoFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	for rel, content := range files {
		writeFile(t, filepath.Join(repo, rel), content)
	}
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

func TestEndToEndFragmentAndSkill(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	repo := gitInitRepoFiles(t, map[string]string{
		"wiz-plugin.yaml": "name: p1demo\n" +
			"prompt_fragments:\n  - \"FRAGMENT MARKER\"\n" +
			"skills:\n  - name: demoskill\n    description: a demo skill\n    instructions: { file: skills/demo.md }\n",
		"skills/demo.md": "SKILL FILE BODY",
	})

	mgr := NewManager(base)
	if _, err := mgr.Install(repo, "", "0.9.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := mgr.SetEnabled("p1demo", true); err != nil {
		t.Fatal(err)
	}

	cfg := &types.Config{Prompt: "BASE"}
	if err := Apply(cfg, base, "0.9.0"); err != nil {
		t.Fatal(err)
	}

	if len(cfg.PromptFragments) != 1 || cfg.PromptFragments[0] != "FRAGMENT MARKER" {
		t.Fatalf("fragment not merged: %+v", cfg.PromptFragments)
	}
	if len(cfg.Skills) != 1 || cfg.Skills[0].Name != "demoskill" || cfg.Skills[0].Instructions != "SKILL FILE BODY" {
		t.Fatalf("skill not merged with file body: %+v", cfg.Skills)
	}
	prompt := cfg.GetPrompt()
	if !strings.Contains(prompt, "FRAGMENT MARKER") {
		t.Fatalf("fragment not in prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "demoskill: a demo skill") || !strings.Contains(prompt, "load_skill") {
		t.Fatalf("skill index not in prompt:\n%s", prompt)
	}
}
