package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/nib/internal/vcs"
	"github.com/mudler/nib/plugin/claudeplugin"
)

// Manager performs plugin install/update/remove against a base directory.
type Manager struct{ baseDir string }

// NewManager returns a Manager rooted at baseDir (use plugin.BaseDir() in prod).
func NewManager(baseDir string) *Manager { return &Manager{baseDir: baseDir} }

// Install clones a plugin, validates its manifest, places it at
// plugins/<name>, and records it in the registry as DISABLED. The caller (CLI)
// enables it after presenting the contribution summary for consent.
func (mgr *Manager) Install(url, ref, nibVersion string) (Manifest, error) {
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
		if err := vcs.CopyDir(url, tmp); err != nil {
			return Manifest{}, fmt.Errorf("copy plugin dir: %w", err)
		}
	} else if err := vcs.Clone(url, ref, tmp); err != nil {
		return Manifest{}, fmt.Errorf("git clone: %w", err)
	}
	if DetectFormat(tmp) == FormatUnknown && claudeplugin.IsMarketplace(tmp) {
		return Manifest{}, fmt.Errorf("%s: %w", url, ErrIsMarketplace)
	}
	m, err := mgr.place(tmp, url, ref, nibVersion)
	if err != nil {
		return Manifest{}, err
	}
	cleanup = false
	return m, nil
}

// place finalizes a populated temp dir tmp into plugins/<manifest name>: it
// validates the manifest, moves the dir into place, and records it DISABLED.
func (mgr *Manager) place(tmp, sourceURL, ref, nibVersion string) (Manifest, error) {
	return mgr.placeEntry(tmp, nibVersion, Entry{SourceURL: sourceURL, Ref: ref})
}

// placeEntry is place with a full registry record template. Its Name and
// Enabled fields are set here.
func (mgr *Manager) placeEntry(tmp, nibVersion string, rec Entry) (Manifest, error) {
	m, err := LoadManifest(tmp, nibVersion)
	if err != nil {
		return Manifest{}, err
	}
	dest := filepath.Join(PluginsDir(mgr.baseDir), m.Name)
	if err := os.RemoveAll(dest); err != nil {
		return Manifest{}, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return Manifest{}, err
	}
	m.root = dest
	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		return Manifest{}, err
	}
	rec.Name = m.Name
	rec.Enabled = false
	reg.Upsert(rec)
	if err := reg.Save(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// InstallDir installs an already-prepared local plugin directory (e.g. an
// unzipped archive) DISABLED. The plugin name comes from its manifest. sourceURL
// is recorded as the plugin's registry provenance (the original .zip path) — not
// dir, which is a throwaway extraction temp dir.
func (mgr *Manager) InstallDir(dir, nibVersion, sourceURL string) (Manifest, error) {
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
	if err := vcs.CopyDir(dir, tmp); err != nil {
		return Manifest{}, fmt.Errorf("copy plugin dir: %w", err)
	}
	m, err := mgr.place(tmp, sourceURL, "", nibVersion)
	if err != nil {
		return Manifest{}, err
	}
	cleanup = false
	return m, nil
}

// Update fast-forwards an installed plugin's checkout. A plugin installed
// from a marketplace is updated by fetching the marketplace again and
// installing the plugin at the commit the marketplace now gives; it keeps its
// enabled state.
func (mgr *Manager) Update(name string) error {
	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		return err
	}
	e := reg.Find(name)
	if e == nil {
		return fmt.Errorf("plugin %q not installed", name)
	}
	if e.Marketplace == "" {
		return vcs.Pull(pluginDir(mgr.baseDir, name))
	}
	ctx := context.Background()
	if _, err := mgr.UpdateMarketplace(ctx, e.Marketplace); err != nil {
		return err
	}
	enabled := e.Enabled
	if _, err := mgr.InstallFromMarketplace(ctx, e.SourceURL, "", ""); err != nil {
		return err
	}
	return mgr.SetEnabled(name, enabled)
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
	return reg.Entries, nil
}
