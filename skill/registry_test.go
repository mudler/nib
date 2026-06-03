package skill

import (
	"path/filepath"
	"testing"
)

func TestRegistryRoundTrip(t *testing.T) {
	base := t.TempDir()

	reg, err := LoadRegistry(base)
	if err != nil {
		t.Fatalf("LoadRegistry empty: %v", err)
	}
	if len(reg.Packs) != 0 {
		t.Fatalf("expected empty registry, got %d", len(reg.Packs))
	}

	reg.Upsert(Entry{Name: "superpowers", SourceURL: "https://x/sp.git", Ref: "v1", Enabled: false})
	reg.Upsert(Entry{Name: "superpowers", SourceURL: "https://x/sp.git", Ref: "v1", Enabled: true}) // update in place
	if len(reg.Packs) != 1 {
		t.Fatalf("upsert should update in place, got %d", len(reg.Packs))
	}
	if err := reg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := LoadRegistry(base)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	e := reloaded.Find("superpowers")
	if e == nil || !e.Enabled {
		t.Fatalf("expected enabled entry after reload, got %+v", e)
	}
	if reloaded.Remove("superpowers"); reloaded.Find("superpowers") != nil {
		t.Fatalf("Remove did not delete entry")
	}

	if registryPath(base) != filepath.Join(base, "skills.yaml") {
		t.Fatalf("registryPath = %q", registryPath(base))
	}
}
