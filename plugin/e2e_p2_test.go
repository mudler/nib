package plugin

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/mudler/nib/slash"
	"github.com/mudler/nib/types"
)

func TestEndToEndCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	repo := gitInitRepoFiles(t, map[string]string{
		"wiz-plugin.yaml": "name: p2demo\n" +
			"commands:\n  - name: review\n    description: review the diff\n    prompt: \"Please review: {{.Args}}\"\n",
	})

	mgr := NewManager(base)
	if _, err := mgr.Install(repo, "", "0.9.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := mgr.SetEnabled("p2demo", true); err != nil {
		t.Fatal(err)
	}

	cfg := &types.Config{}
	if err := Apply(cfg, base, "0.9.0"); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Commands) != 1 || cfg.Commands[0].Name != "review" {
		t.Fatalf("command not merged: %+v", cfg.Commands)
	}

	a := slash.Resolve("/review the auth changes", cfg.Commands, cfg.Skills, cfg.Agents)
	if a.Kind != slash.KindSend || !strings.Contains(a.Text, "Please review: the auth changes") {
		t.Fatalf("command did not expand: %+v", a)
	}
}
