package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/wiz/internal/vcs"
	"github.com/mudler/wiz/types"
)

// Manager performs skill-pack install/update/remove against a base directory.
type Manager struct{ baseDir string }

// NewManager returns a Manager rooted at baseDir (use plugin.BaseDir() in prod).
func NewManager(baseDir string) *Manager { return &Manager{baseDir: baseDir} }

// deriveName turns a git URL or local path into a pack name: the last path
// segment, minus any trailing slash and ".git" suffix.
func deriveName(src string) string {
	s := strings.TrimRight(src, "/")
	s = strings.TrimSuffix(s, ".git")
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// Install clones/copies a skill pack, verifies it contributes at least one
// skill, places it at skills/<name>, and records it in the registry as
// DISABLED. It returns the derived pack name and the harvested skills (with Dir
// set to their final on-disk location).
func (mgr *Manager) Install(src, ref string) (string, []types.Skill, error) {
	skillsDir := SkillsDir(mgr.baseDir)
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return "", nil, err
	}
	tmp, err := os.MkdirTemp(skillsDir, ".tmp-")
	if err != nil {
		return "", nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(tmp)
		}
	}()

	// A local directory installs by copying (no git repo required); anything
	// else (remote URL, scp-style, local git repo) goes through git clone.
	if fi, statErr := os.Stat(src); statErr == nil && fi.IsDir() {
		if err := vcs.CopyDir(src, tmp); err != nil {
			return "", nil, fmt.Errorf("copy skill dir: %w", err)
		}
	} else if err := vcs.Clone(src, ref, tmp); err != nil {
		return "", nil, fmt.Errorf("git clone: %w", err)
	}

	skills, err := HarvestPack(tmp)
	if err != nil {
		return "", nil, err
	}
	if len(skills) == 0 {
		return "", nil, fmt.Errorf("no skills found under skills/*/SKILL.md (did you mean `wiz plugin install`?)")
	}

	name := deriveName(src)
	if name == "" {
		return "", nil, fmt.Errorf("could not derive a pack name from %q", src)
	}

	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		return "", nil, err
	}
	if reg.Find(name) != nil {
		return "", nil, fmt.Errorf("skill pack %q already installed (use `wiz skill update %s` or `wiz skill remove %s`)", name, name, name)
	}

	dest := filepath.Join(skillsDir, name)
	if err := os.RemoveAll(dest); err != nil {
		return "", nil, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", nil, err
	}
	cleanup = false

	reg.Upsert(Entry{Name: name, SourceURL: src, Ref: ref, Enabled: false})
	if err := reg.Save(); err != nil {
		return "", nil, err
	}

	// Re-harvest from the final location so returned Dir paths are correct.
	final, _ := HarvestPack(dest)
	return name, final, nil
}

// Update fast-forwards an installed pack's git checkout. Packs installed from a
// local path have no .git and cannot be updated this way.
func (mgr *Manager) Update(name string) error {
	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		return err
	}
	if reg.Find(name) == nil {
		return fmt.Errorf("skill pack %q not installed", name)
	}
	dir := packDir(mgr.baseDir, name)
	if _, statErr := os.Stat(filepath.Join(dir, ".git")); os.IsNotExist(statErr) {
		return fmt.Errorf("skill pack %q was installed from a local path; nothing to update", name)
	}
	return vcs.Pull(dir)
}

// Remove deletes an installed pack's files and registry record.
func (mgr *Manager) Remove(name string) error {
	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		return err
	}
	if reg.Find(name) == nil {
		return fmt.Errorf("skill pack %q not installed", name)
	}
	if err := os.RemoveAll(packDir(mgr.baseDir, name)); err != nil {
		return err
	}
	reg.Remove(name)
	return reg.Save()
}

// SetEnabled flips a pack's enabled flag in the registry.
func (mgr *Manager) SetEnabled(name string, enabled bool) error {
	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		return err
	}
	e := reg.Find(name)
	if e == nil {
		return fmt.Errorf("skill pack %q not installed", name)
	}
	e.Enabled = enabled
	return reg.Save()
}

// List returns all installed skill packs from the registry.
func (mgr *Manager) List() ([]Entry, error) {
	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		return nil, err
	}
	return reg.Packs, nil
}

// Skills harvests the skills contributed by an installed pack (for `list`).
func (mgr *Manager) Skills(name string) ([]types.Skill, error) {
	return HarvestPack(packDir(mgr.baseDir, name))
}
