package skill

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

func TestEndToEndInstallEnableApply(t *testing.T) {
	base := t.TempDir()
	src := t.TempDir()
	writeSkill(t, src, "brainstorming",
		"---\nname: brainstorming\ndescription: design first\n---\nRead scripts/helper.sh and follow it.\n",
		map[string]string{"scripts/helper.sh": "echo from helper"})

	mgr := NewManager(base)
	name, _, err := mgr.Install(src, "", false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	// Disabled until enabled: Apply yields nothing.
	cfg := &types.Config{}
	if err := Apply(cfg, base); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Skills) != 0 {
		t.Fatalf("disabled pack must not contribute, got %d", len(cfg.Skills))
	}

	// Enable, then Apply contributes the skill with a Dir that contains the
	// supporting script.
	if err := mgr.SetEnabled(name, true); err != nil {
		t.Fatal(err)
	}
	cfg = &types.Config{}
	if err := Apply(cfg, base); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Skills) != 1 {
		t.Fatalf("expected 1 skill after enable, got %d", len(cfg.Skills))
	}
	got := cfg.Skills[0]
	wantDir := filepath.Join(packDir(base, name), "skills", "brainstorming")
	if got.Dir != wantDir {
		t.Fatalf("Dir = %q, want %q", got.Dir, wantDir)
	}
	if !strings.Contains(got.Instructions, "scripts/helper.sh") {
		t.Fatalf("instructions missing reference: %q", got.Instructions)
	}
}
