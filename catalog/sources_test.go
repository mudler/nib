package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourcesRoundTripAndDefaults(t *testing.T) {
	base := t.TempDir()

	// No file yet → the seeded defaults, bundled first and enabled.
	got, err := LoadSources(base)
	if err != nil {
		t.Fatalf("LoadSources: %v", err)
	}
	if len(got) != len(DefaultSources()) {
		t.Fatalf("want %d defaults, got %d", len(DefaultSources()), len(got))
	}
	if got[0].Kind != SourceBundled || !got[0].Enabled {
		t.Fatalf("bundled must be present and enabled: %+v", got[0])
	}

	// Add a user source; it persists and reloads alongside the defaults.
	s, err := AddSource(base, "https://acme.dev/index.json")
	if err != nil {
		t.Fatalf("AddSource: %v", err)
	}
	if s.Kind != SourceIndex || s.Label != "acme.dev" {
		t.Fatalf("AddSource detected wrong: %+v", s)
	}
	got, _ = LoadSources(base)
	if findSource(got, "acme.dev") == nil {
		t.Fatal("user source did not persist")
	}
	if _, err := os.Stat(filepath.Join(base, "sources.yaml")); err != nil {
		t.Fatalf("sources.yaml not written: %v", err)
	}

	// Disabling a default persists and overrides the seed on reload.
	if err := SetSourceEnabled(base, "agentskills.io", false); err != nil {
		t.Fatalf("SetSourceEnabled: %v", err)
	}
	got, _ = LoadSources(base)
	if s := findSource(got, "agentskills.io"); s == nil || s.Enabled {
		t.Fatalf("default override did not persist: %+v", s)
	}
}

func TestBundledSourceIsProtected(t *testing.T) {
	base := t.TempDir()
	if err := RemoveSource(base, "bundled"); err == nil {
		t.Fatal("bundled source must not be removable")
	}
	if err := SetSourceEnabled(base, "bundled", false); err == nil {
		t.Fatal("bundled source must not be disable-able")
	}
	// SaveSources must never persist the bundled entry.
	if err := SaveSources(base, DefaultSources()); err != nil {
		t.Fatalf("SaveSources: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(base, "sources.yaml"))
	if len(data) > 0 && strings.Contains(string(data), "kind: bundled") {
		t.Fatalf("bundled must not be persisted:\n%s", data)
	}
}

func TestBundledIndexParses(t *testing.T) {
	metas, err := ParseIndex(bundledIndex)
	if err != nil {
		t.Fatalf("bundled index does not parse: %v", err)
	}
	if len(metas) == 0 {
		t.Fatal("bundled index should ship at least one starter extension")
	}
}

// findSource returns the source with the given label, or nil.
func findSource(sources []Source, label string) *Source {
	for i := range sources {
		if sources[i].Label == label {
			return &sources[i]
		}
	}
	return nil
}
