package plugin

import (
	"path/filepath"
	"testing"
)

func TestRegistryRoundTrip(t *testing.T) {
	base := t.TempDir()

	reg, err := LoadRegistry(base)
	if err != nil {
		t.Fatalf("LoadRegistry (empty): %v", err)
	}
	if len(reg.Entries) != 0 {
		t.Fatalf("expected empty registry, got %+v", reg.Entries)
	}

	reg.Upsert(Entry{Name: "a", SourceURL: "u1", Ref: "v1", Enabled: true})
	reg.Upsert(Entry{Name: "b", SourceURL: "u2", Enabled: false})
	if err := reg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reg2, err := LoadRegistry(base)
	if err != nil {
		t.Fatalf("LoadRegistry (reload): %v", err)
	}
	if len(reg2.Entries) != 2 {
		t.Fatalf("expected 2 plugins, got %d", len(reg2.Entries))
	}

	// Upsert updates in place (no duplicate).
	reg2.Upsert(Entry{Name: "a", SourceURL: "u1", Ref: "v2", Enabled: false})
	if len(reg2.Entries) != 2 {
		t.Fatalf("upsert duplicated entry: %d", len(reg2.Entries))
	}
	if e := reg2.Find("a"); e == nil || e.Ref != "v2" || e.Enabled {
		t.Fatalf("upsert did not update: %+v", e)
	}

	// Find returns a pointer into the slice (mutation persists after Save).
	reg2.Find("b").Enabled = true
	if err := reg2.Save(); err != nil {
		t.Fatal(err)
	}
	reg3, _ := LoadRegistry(base)
	if !reg3.Find("b").Enabled {
		t.Fatal("in-place mutation via Find not persisted")
	}

	if !reg3.Remove("a") || reg3.Find("a") != nil {
		t.Fatal("Remove failed")
	}
	if reg3.Remove("missing") {
		t.Fatal("Remove of missing entry should return false")
	}

	// Sanity: registry file path is under baseDir.
	if registryPath(base) != filepath.Join(base, "plugins.yaml") {
		t.Fatalf("registryPath = %q", registryPath(base))
	}
}
