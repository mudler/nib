package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/skill"
)

func TestParseInstallArgsForSkill(t *testing.T) {
	// cmd/skill.go reuses parseInstallArgs (defined in cmd/plugin.go) for plugins.
	src, ref, yes, err := parseInstallArgs([]string{"--ref", "v2", "--yes", "/local/path"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if src != "/local/path" || ref != "v2" || !yes {
		t.Fatalf("parsed wrong: src=%q ref=%q yes=%v", src, ref, yes)
	}
	if _, _, _, err := parseInstallArgs([]string{}); err == nil {
		t.Fatalf("expected error on missing source")
	}
}

func TestParseSkillInstallArgsLink(t *testing.T) {
	src, ref, yes, link, err := parseSkillInstallArgs([]string{"--link", "/local/path"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if src != "/local/path" || ref != "" || yes || !link {
		t.Fatalf("parsed wrong: src=%q ref=%q yes=%v link=%v", src, ref, yes, link)
	}
	// --link combined with --ref is rejected.
	if _, _, _, _, err := parseSkillInstallArgs([]string{"--link", "--ref", "v1", "/p"}); err == nil {
		t.Fatalf("expected error combining --link with --ref")
	}
	// Plain install (no --link) still parses.
	src, _, _, link, err = parseSkillInstallArgs([]string{"/local/path"})
	if err != nil || src != "/local/path" || link {
		t.Fatalf("plain parse wrong: src=%q link=%v err=%v", src, link, err)
	}
}

func TestRunSkillCommandUnknownSubcommand(t *testing.T) {
	if code := RunSkillCommand([]string{"frobnicate"}); code != 1 {
		t.Fatalf("expected exit 1 for unknown subcommand, got %d", code)
	}
	if code := RunSkillCommand(nil); code != 1 {
		t.Fatalf("expected exit 1 for no args, got %d", code)
	}
}

func TestSkillListAnnotatesLinked(t *testing.T) {
	base := t.TempDir()
	src := t.TempDir()
	dir := filepath.Join(src, "skills", "s")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: s\ndescription: d\n---\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := skill.NewManager(base)
	name, _, err := mgr.Install(src, "", true)
	if err != nil {
		t.Fatalf("Install link: %v", err)
	}
	if err := mgr.SetEnabled(name, true); err != nil {
		t.Fatal(err)
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	skillList(mgr)
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	if !strings.Contains(string(out), "(linked → ") {
		t.Fatalf("list output missing linked annotation:\n%s", out)
	}
}
