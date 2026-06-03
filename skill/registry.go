package skill

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Entry is one installed skill pack's registry record.
type Entry struct {
	Name      string `yaml:"name"`
	SourceURL string `yaml:"source_url"`
	Ref       string `yaml:"ref"`
	Enabled   bool   `yaml:"enabled"`
}

// Registry is the persisted list of installed skill packs.
type Registry struct {
	Packs []Entry `yaml:"packs"`

	baseDir string // unexported; yaml ignores it
}

// LoadRegistry reads <baseDir>/skills.yaml, returning an empty registry if the
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

// Find returns a pointer to the entry with the given name, or nil.
func (r *Registry) Find(name string) *Entry {
	for i := range r.Packs {
		if r.Packs[i].Name == name {
			return &r.Packs[i]
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
	r.Packs = append(r.Packs, e)
}

// Remove deletes an entry by name, reporting whether one was removed.
func (r *Registry) Remove(name string) bool {
	for i := range r.Packs {
		if r.Packs[i].Name == name {
			r.Packs = append(r.Packs[:i], r.Packs[i+1:]...)
			return true
		}
	}
	return false
}
