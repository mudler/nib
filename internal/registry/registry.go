// Package registry is a generic, YAML-persisted store of named entries shared
// by the plugin and skill-pack installers. Both persist the same record shape
// (Entry) under a single top-level key (e.g. "plugins" or "packs"); the only
// difference is the file name and that key, so the storage logic lives here
// once and is parameterized per caller.
package registry

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Entry is one installed item's registry record. Plugins and skill packs share
// this shape.
type Entry struct {
	Name      string `yaml:"name"`
	SourceURL string `yaml:"source_url"`
	Ref       string `yaml:"ref"`
	Enabled   bool   `yaml:"enabled"`
}

// Registry is a list of entries persisted to <baseDir>/<file> under a single
// top-level YAML key. It is generic over the entry type; nameOf extracts the
// unique name used by Find/Upsert/Remove.
type Registry[E any] struct {
	Entries []E

	baseDir string
	file    string
	key     string
	nameOf  func(E) string
}

// Load reads <baseDir>/<file>, returning an empty registry if the file does not
// exist yet. The file is a single top-level mapping of key → entry list, so an
// unrelated key or a missing file both yield no entries.
func Load[E any](baseDir, file, key string, nameOf func(E) string) (*Registry[E], error) {
	r := &Registry[E]{baseDir: baseDir, file: file, key: key, nameOf: nameOf}
	data, err := os.ReadFile(filepath.Join(baseDir, file))
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, err
	}
	raw := map[string][]E{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	r.Entries = raw[key]
	return r, nil
}

// Save writes the registry back to disk, creating baseDir if needed.
func (r *Registry[E]) Save() error {
	if err := os.MkdirAll(r.baseDir, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(map[string][]E{r.key: r.Entries})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.baseDir, r.file), data, 0o644)
}

// Find returns a pointer to the entry with the given name, or nil. Mutating the
// returned entry mutates the registry in place.
func (r *Registry[E]) Find(name string) *E {
	for i := range r.Entries {
		if r.nameOf(r.Entries[i]) == name {
			return &r.Entries[i]
		}
	}
	return nil
}

// Upsert replaces an existing entry by name, or appends a new one.
func (r *Registry[E]) Upsert(e E) {
	if existing := r.Find(r.nameOf(e)); existing != nil {
		*existing = e
		return
	}
	r.Entries = append(r.Entries, e)
}

// Remove deletes an entry by name, reporting whether one was removed.
func (r *Registry[E]) Remove(name string) bool {
	for i := range r.Entries {
		if r.nameOf(r.Entries[i]) == name {
			r.Entries = append(r.Entries[:i], r.Entries[i+1:]...)
			return true
		}
	}
	return false
}
