package plugin

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Entry is one installed plugin's registry record.
type Entry struct {
	Name      string `yaml:"name"`
	SourceURL string `yaml:"source_url"`
	Ref       string `yaml:"ref"`
	Enabled   bool   `yaml:"enabled"`
}

// Registry is the persisted list of installed plugins.
type Registry struct {
	Plugins []Entry `yaml:"plugins"`

	// baseDir is unexported (yaml ignores it; no struct tag, to keep `go vet` quiet).
	baseDir string
}

// LoadRegistry reads <baseDir>/plugins.yaml, returning an empty registry if the
// file does not exist yet.
func LoadRegistry(baseDir string) (*Registry, error) {
	r := &Registry{baseDir: baseDir}
	data, err := os.ReadFile(registryPath(baseDir))
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, r); err != nil {
		return nil, err
	}
	r.baseDir = baseDir
	return r, nil
}

// Save writes the registry back to disk, creating baseDir if needed.
func (r *Registry) Save() error {
	if err := os.MkdirAll(r.baseDir, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(registryPath(r.baseDir), data, 0o644)
}

// Find returns a pointer to the entry with the given name, or nil. Mutating the
// returned entry mutates the registry in place.
func (r *Registry) Find(name string) *Entry {
	for i := range r.Plugins {
		if r.Plugins[i].Name == name {
			return &r.Plugins[i]
		}
	}
	return nil
}

// Upsert replaces an existing entry by name, or appends a new one.
func (r *Registry) Upsert(e Entry) {
	if existing := r.Find(e.Name); existing != nil {
		*existing = e
		return
	}
	r.Plugins = append(r.Plugins, e)
}

// Remove deletes an entry by name, reporting whether one was removed.
func (r *Registry) Remove(name string) bool {
	for i := range r.Plugins {
		if r.Plugins[i].Name == name {
			r.Plugins = append(r.Plugins[:i], r.Plugins[i+1:]...)
			return true
		}
	}
	return false
}
