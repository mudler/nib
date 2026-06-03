package skill

import "github.com/mudler/nib/internal/registry"

// Entry is one installed skill pack's registry record.
type Entry = registry.Entry

// LoadRegistry reads <baseDir>/skills.yaml, returning an empty registry if the
// file does not exist yet.
func LoadRegistry(baseDir string) (*registry.Registry[Entry], error) {
	return registry.Load(baseDir, "skills.yaml", "packs", func(e Entry) string { return e.Name })
}
