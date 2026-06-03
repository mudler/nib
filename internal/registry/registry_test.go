package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func nameOf(e Entry) string { return e.Name }

func TestRegistryRoundTrip(t *testing.T) {
	base := t.TempDir()

	reg, err := Load(base, "things.yaml", "things", nameOf)
	if err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	if len(reg.Entries) != 0 {
		t.Fatalf("expected empty, got %d", len(reg.Entries))
	}

	reg.Upsert(Entry{Name: "a", SourceURL: "u1", Ref: "v1", Enabled: true})
	reg.Upsert(Entry{Name: "b", SourceURL: "u2"})
	reg.Upsert(Entry{Name: "a", SourceURL: "u1", Ref: "v2"}) // update in place
	if len(reg.Entries) != 2 {
		t.Fatalf("upsert should update in place, got %d", len(reg.Entries))
	}
	if e := reg.Find("a"); e == nil || e.Ref != "v2" || e.Enabled {
		t.Fatalf("upsert did not update: %+v", e)
	}
	if err := reg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Persisted under the configured file and top-level key.
	data, err := os.ReadFile(filepath.Join(base, "things.yaml"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if got := string(data); len(got) == 0 || got[:7] != "things:" {
		t.Fatalf("expected top-level key %q, file was:\n%s", "things:", got)
	}

	reloaded, err := Load(base, "things.yaml", "things", nameOf)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.Entries) != 2 {
		t.Fatalf("expected 2 after reload, got %d", len(reloaded.Entries))
	}

	// Find returns a live pointer; mutation persists across Save.
	reloaded.Find("b").Enabled = true
	if err := reloaded.Save(); err != nil {
		t.Fatal(err)
	}
	again, _ := Load(base, "things.yaml", "things", nameOf)
	if !again.Find("b").Enabled {
		t.Fatal("in-place mutation via Find not persisted")
	}

	if !again.Remove("a") || again.Find("a") != nil {
		t.Fatal("Remove failed")
	}
	if again.Remove("missing") {
		t.Fatal("Remove of missing entry should return false")
	}
}

func TestLoadIgnoresOtherKeys(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "things.yaml"),
		[]byte("other:\n  - name: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := Load(base, "things.yaml", "things", nameOf)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(reg.Entries) != 0 {
		t.Fatalf("entries under a different key must not be read, got %d", len(reg.Entries))
	}
}
