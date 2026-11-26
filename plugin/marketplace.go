package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/nib/internal/registry"
	"github.com/mudler/nib/plugin/claudeplugin"
)

// ErrIsMarketplace is returned by Install for a source that holds a
// marketplace (.claude-plugin/marketplace.json) instead of a plugin. Add it
// with AddMarketplace and install its plugins as name@marketplace.
var ErrIsMarketplace = errors.New("source is a plugin marketplace, not a plugin")

// MarketplaceRecord is one registered marketplace.
type MarketplaceRecord struct {
	Name string `yaml:"name"`
	// Source is the location as the user gave it (see
	// claudeplugin.ParseMarketplaceSource).
	Source string `yaml:"source"`
	// SHA is the commit of the cached copy, when the source is git.
	SHA string `yaml:"sha,omitempty"`
}

// MarketplacesDir is where marketplace copies are cached.
func MarketplacesDir(baseDir string) string { return filepath.Join(baseDir, "marketplaces") }

func loadMarketplaces(baseDir string) (*registry.Registry[MarketplaceRecord], error) {
	return registry.Load(baseDir, "marketplaces.yaml", "marketplaces", func(r MarketplaceRecord) string { return r.Name })
}

// Marketplaces lists the registered marketplaces.
func (mgr *Manager) Marketplaces() ([]MarketplaceRecord, error) {
	reg, err := loadMarketplaces(mgr.baseDir)
	if err != nil {
		return nil, err
	}
	return reg.Entries, nil
}

// AddMarketplace fetches a marketplace (a directory, owner/repo, a git URL
// or a marketplace.json URL) into the cache and registers it under its own
// name. Adding a marketplace with the name of a registered one replaces it.
func (mgr *Manager) AddMarketplace(ctx context.Context, src string) (*claudeplugin.Marketplace, error) {
	ms, err := claudeplugin.ParseMarketplaceSource(src)
	if err != nil {
		return nil, err
	}
	return mgr.fetchMarketplace(ctx, ms)
}

func (mgr *Manager) fetchMarketplace(ctx context.Context, ms claudeplugin.MarketplaceSource) (*claudeplugin.Marketplace, error) {
	dir := MarketplacesDir(mgr.baseDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(dir, ".tmp-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	m, err := claudeplugin.FetchMarketplace(ctx, ms, filepath.Join(tmp, "m"), claudeplugin.FetchOptions{})
	if err != nil {
		return nil, err
	}
	reg, err := loadMarketplaces(mgr.baseDir)
	if err != nil {
		return nil, err
	}
	// The name comes from the fetched file. A different source must not
	// take over the cache, and the updates, of a registered marketplace.
	if prev := reg.Find(m.Name); prev != nil && prev.Source != ms.String() {
		return nil, fmt.Errorf("a marketplace named %q is already registered from %s; remove it first", m.Name, prev.Source)
	}
	dest := filepath.Join(dir, m.Name)
	if err := os.RemoveAll(dest); err != nil {
		return nil, err
	}
	if err := os.Rename(filepath.Join(tmp, "m"), dest); err != nil {
		return nil, err
	}
	if m.Root != "" {
		m.Root = dest
	}
	reg.Upsert(MarketplaceRecord{Name: m.Name, Source: ms.String(), SHA: m.SHA})
	return m, reg.Save()
}

// Marketplace loads a registered marketplace from the cache.
func (mgr *Manager) Marketplace(name string) (*claudeplugin.Marketplace, error) {
	reg, err := loadMarketplaces(mgr.baseDir)
	if err != nil {
		return nil, err
	}
	rec := reg.Find(name)
	if rec == nil {
		return nil, fmt.Errorf("marketplace %q is not registered", name)
	}
	m, err := claudeplugin.LoadMarketplace(filepath.Join(MarketplacesDir(mgr.baseDir), name))
	if err != nil {
		return nil, err
	}
	m.SHA = rec.SHA
	if ms, err := claudeplugin.ParseMarketplaceSource(rec.Source); err == nil && ms.Kind == "url" {
		m.Root = "" // only marketplace.json was fetched
	}
	return m, nil
}

// UpdateMarketplace fetches a registered marketplace again from its source.
func (mgr *Manager) UpdateMarketplace(ctx context.Context, name string) (*claudeplugin.Marketplace, error) {
	reg, err := loadMarketplaces(mgr.baseDir)
	if err != nil {
		return nil, err
	}
	rec := reg.Find(name)
	if rec == nil {
		return nil, fmt.Errorf("marketplace %q is not registered", name)
	}
	ms, err := claudeplugin.ParseMarketplaceSource(rec.Source)
	if err != nil {
		return nil, err
	}
	m, err := mgr.fetchMarketplace(ctx, ms)
	if err != nil {
		return nil, err
	}
	if m.Name != name {
		return nil, fmt.Errorf("marketplace %q now calls itself %q; remove it and add it again", name, m.Name)
	}
	return m, nil
}

// RemoveMarketplace unregisters a marketplace and deletes its cached copy.
// Plugins installed from it stay installed.
func (mgr *Manager) RemoveMarketplace(name string) error {
	reg, err := loadMarketplaces(mgr.baseDir)
	if err != nil {
		return err
	}
	if !reg.Remove(name) {
		return fmt.Errorf("marketplace %q is not registered", name)
	}
	if err := os.RemoveAll(filepath.Join(MarketplacesDir(mgr.baseDir), name)); err != nil {
		return err
	}
	return reg.Save()
}

// InstallFromMarketplace installs plugin id ("name@marketplace") from a
// registered marketplace, DISABLED. sha pins the plugin to a commit; empty
// uses the entry's own sha, then its ref, then the default branch. The commit
// actually installed is recorded in the registry.
func (mgr *Manager) InstallFromMarketplace(ctx context.Context, id, sha, nibVersion string) (Manifest, error) {
	name, mktName, ok := claudeplugin.ParsePluginID(id)
	if !ok {
		return Manifest{}, fmt.Errorf("%q is not a name@marketplace plugin id", id)
	}
	m, err := mgr.Marketplace(mktName)
	if err != nil {
		return Manifest{}, err
	}
	e, err := m.Find(name)
	if err != nil {
		return Manifest{}, err
	}
	if err := e.Source.Supported(); err != nil {
		return Manifest{}, fmt.Errorf("plugin %s: %w", id, err)
	}
	pluginsDir := PluginsDir(mgr.baseDir)
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		return Manifest{}, err
	}
	tmp, err := os.MkdirTemp(pluginsDir, ".tmp-")
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(tmp)
	dest := filepath.Join(tmp, "plugin")
	res, err := claudeplugin.Fetch(ctx, m, e, dest, claudeplugin.FetchOptions{SHA: sha})
	if err != nil {
		return Manifest{}, err
	}
	if err := writeSourceRecord(dest, sourceRecord{Marketplace: m.Name, Entry: e.Raw, SHA: res.SHA}); err != nil {
		return Manifest{}, err
	}
	return mgr.placeEntry(dest, nibVersion, Entry{
		SourceURL:   e.Name + "@" + m.Name,
		Marketplace: m.Name,
		SHA:         res.SHA,
	})
}
