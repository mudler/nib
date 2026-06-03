package plugin

import "github.com/mudler/nib/internal/registry"

// Entry is one installed plugin's registry record.
type Entry = registry.Entry

// LoadRegistry reads <baseDir>/plugins.yaml, returning an empty registry if the
// file does not exist yet.
func LoadRegistry(baseDir string) (*registry.Registry[Entry], error) {
	return registry.Load(baseDir, "plugins.yaml", "plugins", func(e Entry) string { return e.Name })
}
