package catalog

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// bundledIndex is the starter catalog compiled into the binary; it backs the
// always-on "bundled" source so browse works with zero configuration and no
// network.
//
//go:embed bundled_index.json
var bundledIndex []byte

// sourcesFile is where user source configuration persists, next to the skill
// and plugin registries.
const sourcesFile = "sources.yaml"

// bundledLabel is the reserved label of the non-removable bundled source.
const bundledLabel = "bundled"

// sourcesDoc is the on-disk shape of sources.yaml.
type sourcesDoc struct {
	Sources []Source `yaml:"sources"`
}

// DefaultSources returns the seeded sources every install starts with: the
// always-on bundled starter index, the official agentskills.io well-known
// catalog, and the openclaw/agent-skills community GitHub catalog.
func DefaultSources() []Source {
	return []Source{
		{Label: bundledLabel, URL: "", Kind: SourceBundled, Trust: "official", Enabled: true},
		{Label: "agentskills.io", URL: "https://agentskills.io", Kind: SourceWellKnown, Trust: "official", Enabled: true},
		{Label: "openclaw/agent-skills", URL: "https://github.com/openclaw/agent-skills", Kind: SourceGitHub, Trust: "community", Enabled: true},
	}
}

// LoadSources returns the effective source list: the seeded defaults overlaid
// with any user-added or user-modified sources from <baseDir>/sources.yaml. A
// persisted source with the same Label as a default overrides it (so a user can
// disable a default), except the bundled source, which is always present and
// enabled and can never be overridden.
func LoadSources(baseDir string) ([]Source, error) {
	persisted, err := readSources(baseDir)
	if err != nil {
		return nil, err
	}
	byLabel := map[string]Source{}
	var order []string
	add := func(s Source) {
		if _, ok := byLabel[s.Label]; !ok {
			order = append(order, s.Label)
		}
		byLabel[s.Label] = s
	}
	for _, s := range DefaultSources() {
		add(s)
	}
	for _, s := range persisted {
		if s.Kind == SourceBundled || s.Label == bundledLabel {
			continue // bundled is never user-overridable
		}
		add(s)
	}
	out := make([]Source, 0, len(order))
	for _, l := range order {
		s := byLabel[l]
		if s.Kind == SourceBundled {
			s.Enabled = true // re-assert the invariant
		}
		out = append(out, s)
	}
	return out, nil
}

// SaveSources writes the user-configurable sources to <baseDir>/sources.yaml.
// The bundled source is never persisted (LoadSources always re-injects it), so
// it cannot be edited away on disk.
func SaveSources(baseDir string, sources []Source) error {
	var persist []Source
	for _, s := range sources {
		if s.Kind == SourceBundled || s.Label == bundledLabel {
			continue
		}
		persist = append(persist, s)
	}
	data, err := yaml.Marshal(sourcesDoc{Sources: persist})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(baseDir, sourcesFile), data, 0o644)
}

// AddSource detects the kind/label of a pasted URL, appends it as an enabled
// community source, and persists it. It errors if a source with that label
// already exists.
func AddSource(baseDir, url string) (Source, error) {
	kind, label := DetectSourceKind(url)
	sources, err := LoadSources(baseDir)
	if err != nil {
		return Source{}, err
	}
	for _, ex := range sources {
		if ex.Label == label {
			return Source{}, fmt.Errorf("source %q already exists", label)
		}
	}
	s := Source{Label: label, URL: url, Kind: kind, Trust: "community", Enabled: true}
	return s, SaveSources(baseDir, append(sources, s))
}

// RemoveSource removes a source by label. The bundled source cannot be removed.
func RemoveSource(baseDir, label string) error {
	if label == bundledLabel {
		return fmt.Errorf("the bundled source cannot be removed")
	}
	sources, err := LoadSources(baseDir)
	if err != nil {
		return err
	}
	var kept []Source
	found := false
	for _, s := range sources {
		if s.Label == label {
			found = true
			continue
		}
		kept = append(kept, s)
	}
	if !found {
		return fmt.Errorf("no source labeled %q", label)
	}
	return SaveSources(baseDir, kept)
}

// SetSourceEnabled toggles a source by label and persists the change. The
// bundled source is always enabled and cannot be disabled.
func SetSourceEnabled(baseDir, label string, enabled bool) error {
	if label == bundledLabel {
		return fmt.Errorf("the bundled source cannot be disabled")
	}
	sources, err := LoadSources(baseDir)
	if err != nil {
		return err
	}
	found := false
	for i := range sources {
		if sources[i].Label == label {
			sources[i].Enabled = enabled
			found = true
		}
	}
	if !found {
		return fmt.Errorf("no source labeled %q", label)
	}
	return SaveSources(baseDir, sources)
}

// readSources loads the persisted sources, treating a missing file as empty.
func readSources(baseDir string) ([]Source, error) {
	data, err := os.ReadFile(filepath.Join(baseDir, sourcesFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var doc sourcesDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc.Sources, nil
}
