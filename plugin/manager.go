package plugin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// gitClone clones url (optionally at ref) into dest. Var for test injection.
var gitClone = func(url, ref, dest string) error {
	args := []string{"clone", "--depth", "1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, url, dest)
	cmd := exec.Command("git", args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// gitPull fast-forwards an existing checkout. Var for test injection.
var gitPull = func(dir string) error {
	cmd := exec.Command("git", "-C", dir, "pull", "--ff-only")
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Manager performs plugin install/update/remove against a base directory.
type Manager struct{ baseDir string }

// NewManager returns a Manager rooted at baseDir (use plugin.BaseDir() in prod).
func NewManager(baseDir string) *Manager { return &Manager{baseDir: baseDir} }

// copyDir recursively copies the contents of src into dst (which must already
// exist), skipping any .git directory and preserving file permission bits (so
// hook scripts stay executable). Used to install a plugin from a local
// directory without requiring it to be a git repository.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if info, e := d.Info(); e == nil {
			mode = info.Mode().Perm()
		}
		return os.WriteFile(filepath.Join(dst, rel), data, mode)
	})
}

// Install clones a plugin, validates its manifest, places it at
// plugins/<name>, and records it in the registry as DISABLED. The caller (CLI)
// enables it after presenting the contribution summary for consent.
func (mgr *Manager) Install(url, ref, wizVersion string) (Manifest, error) {
	pluginsDir := PluginsDir(mgr.baseDir)
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		return Manifest{}, err
	}
	tmp, err := os.MkdirTemp(pluginsDir, ".tmp-")
	if err != nil {
		return Manifest{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(tmp)
		}
	}()

	// A local directory installs by copying (no git repo required); anything
	// else (remote URL, local git repo, scp-style) goes through git clone.
	if fi, statErr := os.Stat(url); statErr == nil && fi.IsDir() {
		if err := copyDir(url, tmp); err != nil {
			return Manifest{}, fmt.Errorf("copy plugin dir: %w", err)
		}
	} else if err := gitClone(url, ref, tmp); err != nil {
		return Manifest{}, fmt.Errorf("git clone: %w", err)
	}
	m, err := LoadManifest(tmp, wizVersion)
	if err != nil {
		return Manifest{}, err
	}

	dest := filepath.Join(pluginsDir, m.Name)
	if err := os.RemoveAll(dest); err != nil {
		return Manifest{}, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return Manifest{}, err
	}
	cleanup = false
	m.root = dest

	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		return Manifest{}, err
	}
	reg.Upsert(Entry{Name: m.Name, SourceURL: url, Ref: ref, Enabled: false})
	if err := reg.Save(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Update fast-forwards an installed plugin's checkout.
func (mgr *Manager) Update(name string) error {
	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		return err
	}
	if reg.Find(name) == nil {
		return fmt.Errorf("plugin %q not installed", name)
	}
	return gitPull(pluginDir(mgr.baseDir, name))
}

// Remove deletes an installed plugin's files and registry record.
func (mgr *Manager) Remove(name string) error {
	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		return err
	}
	if reg.Find(name) == nil {
		return fmt.Errorf("plugin %q not installed", name)
	}
	if err := os.RemoveAll(pluginDir(mgr.baseDir, name)); err != nil {
		return err
	}
	reg.Remove(name)
	return reg.Save()
}

// SetEnabled flips a plugin's enabled flag in the registry.
func (mgr *Manager) SetEnabled(name string, enabled bool) error {
	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		return err
	}
	e := reg.Find(name)
	if e == nil {
		return fmt.Errorf("plugin %q not installed", name)
	}
	e.Enabled = enabled
	return reg.Save()
}

// List returns all installed plugins from the registry.
func (mgr *Manager) List() ([]Entry, error) {
	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		return nil, err
	}
	return reg.Plugins, nil
}
