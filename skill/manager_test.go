package skill

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/nib/internal/vcs"
	"github.com/mudler/nib/types"
)

func TestDeriveName(t *testing.T) {
	cases := map[string]string{
		"https://github.com/obra/superpowers":     "superpowers",
		"https://github.com/obra/superpowers.git": "superpowers",
		"https://github.com/obra/superpowers/":    "superpowers",
		"git@github.com:obra/superpowers.git":     "superpowers",
		"/home/me/my-skills":                      "my-skills",
		"/home/me/my-skills/":                     "my-skills",
	}
	for in, want := range cases {
		if got := deriveName(in); got != want {
			t.Errorf("deriveName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestManagerInstallFromLocalDir(t *testing.T) {
	base := t.TempDir()
	src := t.TempDir()
	writeSkill(t, src, "brainstorming",
		"---\nname: brainstorming\ndescription: design first\n---\nask questions\n", nil)

	mgr := NewManager(base)
	name, skills, err := mgr.Install(src, "", false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(skills) != 1 || skills[0].Name != "brainstorming" {
		t.Fatalf("expected 1 harvested skill, got %+v", skills)
	}
	// Files landed at skills/<name>.
	if _, err := os.Stat(filepath.Join(packDir(base, name), "skills", "brainstorming", "SKILL.md")); err != nil {
		t.Fatalf("pack files not at expected dir: %v", err)
	}
	// Returned Dir points at the final on-disk location, not the temp dir.
	wantDir := filepath.Join(packDir(base, name), "skills", "brainstorming")
	if skills[0].Dir != wantDir {
		t.Fatalf("Dir = %q, want %q", skills[0].Dir, wantDir)
	}
	// Registry records it, disabled.
	reg, _ := LoadRegistry(base)
	e := reg.Find(name)
	if e == nil || e.Enabled || e.SourceURL != src {
		t.Fatalf("registry entry wrong: %+v", e)
	}

	// SetEnabled flips the flag.
	if err := mgr.SetEnabled(name, true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	reg, _ = LoadRegistry(base)
	if !reg.Find(name).Enabled {
		t.Fatalf("expected enabled after SetEnabled")
	}

	// Re-installing the same name is rejected.
	if _, _, err := mgr.Install(src, "", false); err == nil {
		t.Fatalf("expected collision error on re-install")
	}

	// Remove deletes files and registry record.
	if err := mgr.Remove(name); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(packDir(base, name)); !os.IsNotExist(err) {
		t.Fatalf("pack dir should be gone")
	}
}

func TestManagerInstallLink(t *testing.T) {
	base := t.TempDir()
	src := t.TempDir()
	writeSkill(t, src, "brainstorming",
		"---\nname: brainstorming\ndescription: design first\n---\nask questions\n", nil)

	mgr := NewManager(base)
	name, skills, err := mgr.Install(src, "", true)
	if err != nil {
		t.Fatalf("Install link: %v", err)
	}
	if len(skills) != 1 || skills[0].Name != "brainstorming" {
		t.Fatalf("expected 1 harvested skill, got %+v", skills)
	}
	// Pack dir is a symlink to abs(src), not a copy.
	fi, err := os.Lstat(packDir(base, name))
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected pack dir to be a symlink, got %v (err %v)", fi.Mode(), err)
	}
	absSrc, _ := filepath.Abs(src)
	target, _ := os.Readlink(packDir(base, name))
	if target != absSrc {
		t.Fatalf("symlink target = %q, want %q", target, absSrc)
	}
	// LinkTarget reports it.
	if got, linked := mgr.LinkTarget(name); !linked || got != absSrc {
		t.Fatalf("LinkTarget = (%q, %v), want (%q, true)", got, linked, absSrc)
	}
	// Registry records it, disabled, with the absolute source.
	reg, _ := LoadRegistry(base)
	if e := reg.Find(name); e == nil || e.Enabled || e.SourceURL != absSrc {
		t.Fatalf("registry entry wrong: %+v", e)
	}
	// Live edit: a new skill added to the source surfaces on re-harvest.
	writeSkill(t, src, "newone", "---\nname: newone\ndescription: fresh\n---\nbody\n", nil)
	live, _ := HarvestPack(packDir(base, name))
	if len(live) != 2 {
		t.Fatalf("expected live edit to surface 2 skills, got %d", len(live))
	}
}

func TestManagerInstallLinkEnabledSurfacesViaApply(t *testing.T) {
	base := t.TempDir()
	src := t.TempDir()
	writeSkill(t, src, "brainstorming",
		"---\nname: brainstorming\ndescription: design first\n---\nask questions\n", nil)

	mgr := NewManager(base)
	name, _, err := mgr.Install(src, "", true)
	if err != nil {
		t.Fatalf("Install link: %v", err)
	}
	if err := mgr.SetEnabled(name, true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}

	cfg := types.Config{}
	if err := Apply(&cfg, base); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	found := false
	for _, s := range cfg.Skills {
		if s.Name == "brainstorming" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("linked pack skill not surfaced via Apply, got %+v", cfg.Skills)
	}
}

func TestManagerInstallLinkRejectsNonDir(t *testing.T) {
	base := t.TempDir()
	mgr := NewManager(base)
	// A git URL is not a local directory.
	if _, _, err := mgr.Install("https://example.com/x.git", "", true); err == nil {
		t.Fatalf("expected error linking a non-directory source")
	}
	// A nonexistent path.
	if _, _, err := mgr.Install(filepath.Join(base, "nope"), "", true); err == nil {
		t.Fatalf("expected error linking a nonexistent path")
	}
}

func TestLinkTargetFalseForCopy(t *testing.T) {
	base := t.TempDir()
	src := t.TempDir()
	writeSkill(t, src, "s", "---\nname: s\ndescription: d\n---\nb\n", nil)
	mgr := NewManager(base)
	name, _, err := mgr.Install(src, "", false)
	if err != nil {
		t.Fatalf("Install copy: %v", err)
	}
	if got, linked := mgr.LinkTarget(name); linked {
		t.Fatalf("LinkTarget on a copied pack = (%q, true), want linked=false", got)
	}
}

func TestManagerInstallRejectsNoSkills(t *testing.T) {
	base := t.TempDir()
	src := t.TempDir() // empty: no skills/ dir
	mgr := NewManager(base)
	if _, _, err := mgr.Install(src, "", false); err == nil {
		t.Fatalf("expected error when no skills found")
	}
}

func TestValidPackName(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "a/b", "../evil", `a\b`} {
		if validPackName(bad) {
			t.Errorf("validPackName(%q) = true, want false", bad)
		}
	}
	for _, good := range []string{"superpowers", "my-skills", "skills.v2"} {
		if !validPackName(good) {
			t.Errorf("validPackName(%q) = false, want true", good)
		}
	}
}

func TestManagerInstallRejectsUnsafeSourceWithoutDestroyingBase(t *testing.T) {
	base := t.TempDir()
	// A sentinel that must survive a rejected install.
	sentinel := filepath.Join(base, "skills.yaml")
	if err := os.WriteFile(sentinel, []byte("packs: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A real directory whose path ends in ".." — os.Stat sees a dir (the parent),
	// and deriveName("<dir>/sub/..") yields "..".
	realDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(realDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Build the path manually (filepath.Join would Clean away the trailing "..").
	unsafeSrc := filepath.Join(realDir, "sub") + string(filepath.Separator) + ".."
	if deriveName(unsafeSrc) != ".." {
		t.Fatalf("precondition: deriveName(%q) = %q, want \"..\"", unsafeSrc, deriveName(unsafeSrc))
	}

	mgr := NewManager(base)
	if _, _, err := mgr.Install(unsafeSrc, "", false); err == nil {
		t.Fatalf("expected Install to reject unsafe source")
	}
	// The base config (sentinel) must be untouched.
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("install wiped base config: %v", err)
	}
}

func TestManagerInstallFromGitClone(t *testing.T) {
	base := t.TempDir()

	orig := vcs.Clone
	vcs.Clone = func(url, ref, dest string) error {
		// Materialize a pack with one skill into the clone destination.
		dir := filepath.Join(dest, "skills", "demo")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, "SKILL.md"),
			[]byte("---\nname: demo\ndescription: d\n---\nbody\n"), 0o644)
	}
	t.Cleanup(func() { vcs.Clone = orig })

	mgr := NewManager(base)
	name, skills, err := mgr.Install("https://example.com/demo.git", "v1", false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if name != "demo" || len(skills) != 1 || skills[0].Name != "demo" {
		t.Fatalf("unexpected install result: name=%q skills=%+v", name, skills)
	}
	reg, _ := LoadRegistry(base)
	e := reg.Find("demo")
	if e == nil || e.Ref != "v1" || e.Enabled {
		t.Fatalf("registry entry wrong: %+v", e)
	}
}

func TestUpdateOnLinkedPackIsNoOp(t *testing.T) {
	base := t.TempDir()
	src := t.TempDir()
	writeSkill(t, src, "s", "---\nname: s\ndescription: d\n---\nb\n", nil)
	mgr := NewManager(base)
	name, _, err := mgr.Install(src, "", true)
	if err != nil {
		t.Fatalf("Install link: %v", err)
	}
	if err := mgr.Update(name); err != nil {
		t.Fatalf("Update on linked pack should be a no-op, got: %v", err)
	}
	if _, linked := mgr.LinkTarget(name); !linked {
		t.Fatalf("pack should still be linked after Update")
	}
}

func TestRemoveLinkedPackLeavesTargetIntact(t *testing.T) {
	base := t.TempDir()
	src := t.TempDir()
	writeSkill(t, src, "s", "---\nname: s\ndescription: d\n---\nb\n", nil)
	mgr := NewManager(base)
	name, _, err := mgr.Install(src, "", true)
	if err != nil {
		t.Fatalf("Install link: %v", err)
	}
	if err := mgr.Remove(name); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	// Symlink gone.
	if _, err := os.Lstat(packDir(base, name)); !os.IsNotExist(err) {
		t.Fatalf("symlink should be removed")
	}
	// Target files untouched.
	if _, err := os.Stat(filepath.Join(src, "skills", "s", "SKILL.md")); err != nil {
		t.Fatalf("link target was wiped: %v", err)
	}
	// Registry record gone.
	reg, _ := LoadRegistry(base)
	if reg.Find(name) != nil {
		t.Fatalf("registry still has the removed pack")
	}
}
