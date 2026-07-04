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

func TestBuiltInSourcesCannotBeRemoved(t *testing.T) {
	// Every built-in default is a fixed member of the set: removing it must
	// fail, and because LoadSources re-seeds the defaults, the source must
	// still be present (and untouched) after a failed remove + reload.
	for _, label := range []string{"agentskills.io", "openclaw/agent-skills"} {
		base := t.TempDir()
		if err := RemoveSource(base, label); err == nil {
			t.Fatalf("built-in source %q must not be removable", label)
		}
		// The failed remove must not have rewritten sources.yaml.
		if _, err := os.Stat(filepath.Join(base, "sources.yaml")); !os.IsNotExist(err) {
			t.Fatalf("rejected remove of %q must not write sources.yaml (err=%v)", label, err)
		}
		got, err := LoadSources(base)
		if err != nil {
			t.Fatalf("LoadSources: %v", err)
		}
		if s := findSource(got, label); s == nil {
			t.Fatalf("built-in source %q vanished after rejected remove", label)
		} else if !s.Enabled {
			t.Fatalf("built-in source %q must stay enabled after rejected remove: %+v", label, s)
		}
	}

	// Disable is the supported way to turn a built-in default off.
	base := t.TempDir()
	if err := SetSourceEnabled(base, "agentskills.io", false); err != nil {
		t.Fatalf("SetSourceEnabled(agentskills.io, false): %v", err)
	}
	got, _ := LoadSources(base)
	if s := findSource(got, "agentskills.io"); s == nil || s.Enabled {
		t.Fatalf("disable of built-in default did not persist: %+v", s)
	}
}

func TestUserAddedSourceCanBeRemoved(t *testing.T) {
	base := t.TempDir()
	if _, err := AddSource(base, "https://acme.dev/index.json"); err != nil {
		t.Fatalf("AddSource: %v", err)
	}
	if err := RemoveSource(base, "acme.dev"); err != nil {
		t.Fatalf("user-added source must be removable: %v", err)
	}
	got, _ := LoadSources(base)
	if findSource(got, "acme.dev") != nil {
		t.Fatal("user-added source reappeared after remove + reload")
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
