package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/nib/internal/vcs"
	"github.com/mudler/nib/types"
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

// validPackName reports whether name is a single, safe path segment usable as a
// directory under skills/ — no path separators and not "."/".." (which would
// let a crafted source escape the skills directory).
func validPackName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

// Install clones/copies a skill pack, verifies it contributes at least one
// skill, places it at skills/<name>, and records it in the registry as
// DISABLED. It returns the derived pack name and the harvested skills (with Dir
// set to their final on-disk location).
func (mgr *Manager) Install(src, ref string, link bool) (string, []types.Skill, error) {
	name := deriveName(src)
	if !validPackName(name) {
		return "", nil, fmt.Errorf("could not derive a valid pack name from %q", src)
	}
	if link {
		return mgr.installLink(src, name)
	}

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
		return "", nil, fmt.Errorf("no SKILL.md found under %s (did you mean `nib plugin install`?)", src)
	}

	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		return "", nil, err
	}
	if reg.Find(name) != nil {
		return "", nil, fmt.Errorf("skill pack %q already installed (use `nib skill update %s` or `nib skill remove %s`)", name, name, name)
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
	// The discarded error is safe: this same content was harvested successfully
	// moments earlier from the temp dir; we re-harvest only to get Dir paths
	// rooted at the final location.
	final, _ := HarvestPack(dest)
	return name, final, nil
}

// installLink symlinks an existing local directory into the skills dir instead
// of copying it, so edits to the source surface on the next nib launch. It
// verifies the source contributes at least one skill before recording it
// DISABLED in the registry, with the absolute source path as SourceURL.
func (mgr *Manager) installLink(src, name string) (string, []types.Skill, error) {
	fi, err := os.Stat(src)
	if err != nil || !fi.IsDir() {
		return "", nil, fmt.Errorf("--link requires an existing local directory: %q", src)
	}
	abs, err := filepath.Abs(src)
	if err != nil {
		return "", nil, err
	}
	skills, err := HarvestPack(abs)
	if err != nil {
		return "", nil, err
	}
	if len(skills) == 0 {
		return "", nil, fmt.Errorf("no SKILL.md found under %s", abs)
	}
	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		return "", nil, err
	}
	if reg.Find(name) != nil {
		return "", nil, fmt.Errorf("skill pack %q already installed (use `nib skill update %s` or `nib skill remove %s`)", name, name, name)
	}
	if err := os.MkdirAll(SkillsDir(mgr.baseDir), 0o755); err != nil {
		return "", nil, err
	}
	dest := packDir(mgr.baseDir, name)
	if err := os.Symlink(abs, dest); err != nil {
		return "", nil, err
	}
	reg.Upsert(Entry{Name: name, SourceURL: abs, Ref: "", Enabled: false})
	if err := reg.Save(); err != nil {
		os.Remove(dest) // roll back the symlink if the registry can't be written
		return "", nil, err
	}
	// Re-harvest from the final location so returned Dir paths resolve through
	// the symlink (consistent with the copy path).
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
	if _, linked := mgr.LinkTarget(name); linked {
		return nil // a linked pack reads its source live; nothing to fetch
	}
	dir := packDir(mgr.baseDir, name)
	if _, statErr := os.Stat(filepath.Join(dir, ".git")); os.IsNotExist(statErr) {
		return fmt.Errorf("skill pack %q was installed from a local path; nothing to update", name)
	}
	return vcs.Pull(dir)
}

// LinkTarget reports whether an installed pack is a symlink (a --link install)
// and, if so, the path it points at.
func (mgr *Manager) LinkTarget(name string) (string, bool) {
	dir := packDir(mgr.baseDir, name)
	fi, err := os.Lstat(dir)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return "", false
	}
	target, err := os.Readlink(dir)
	if err != nil {
		return "", false
	}
	return target, true
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
	dir := packDir(mgr.baseDir, name)
	if _, linked := mgr.LinkTarget(name); linked {
		if err := os.Remove(dir); err != nil { // unlink only; never the target
			return err
		}
	} else if err := os.RemoveAll(dir); err != nil {
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
	return reg.Entries, nil
}

// Skills harvests the skills contributed by an installed pack (for `list`).
func (mgr *Manager) Skills(name string) ([]types.Skill, error) {
	return HarvestPack(packDir(mgr.baseDir, name))
}
