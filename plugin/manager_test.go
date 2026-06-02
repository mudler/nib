package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

// withFakeGit replaces gitClone to materialize a fixed manifest, so Manager
// tests don't depend on a network or real remote.
func withFakeGit(t *testing.T, manifestBody string) {
	t.Helper()
	origClone := gitClone
	gitClone = func(url, ref, dest string) error {
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dest, NativeManifestFile), []byte(manifestBody), 0o644)
	}
	t.Cleanup(func() { gitClone = origClone })
}

func TestManagerInstallRegistersDisabled(t *testing.T) {
	base := t.TempDir()
	withFakeGit(t, "name: demo\nversion: 1.2.3\n")

	mgr := NewManager(base)
	m, err := mgr.Install("https://example.com/demo.git", "v1", "0.9.0")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if m.Name != "demo" {
		t.Fatalf("manifest name = %q", m.Name)
	}
	// Files landed at plugins/<name>.
	if _, err := os.Stat(filepath.Join(pluginDir(base, "demo"), NativeManifestFile)); err != nil {
		t.Fatalf("plugin files not at expected dir: %v", err)
	}
	// Registry records it, disabled, with source+ref.
	reg, _ := LoadRegistry(base)
	e := reg.Find("demo")
	if e == nil || e.Enabled {
		t.Fatalf("expected disabled registry entry, got %+v", e)
	}
	if e.SourceURL != "https://example.com/demo.git" || e.Ref != "v1" {
		t.Fatalf("registry source/ref wrong: %+v", e)
	}
}

func TestManagerInstallRejectsBadManifest(t *testing.T) {
	base := t.TempDir()
	withFakeGit(t, "version: 1.0.0\n") // no name → invalid
	if _, err := NewManager(base).Install("u", "", "0.9.0"); err == nil {
		t.Fatal("expected install to reject manifest with no name")
	}
	// No temp dirs left behind.
	entries, _ := os.ReadDir(PluginsDir(base))
	if len(entries) != 0 {
		t.Fatalf("temp clone not cleaned up: %v", entries)
	}
}

func TestManagerSetEnabledRemoveList(t *testing.T) {
	base := t.TempDir()
	withFakeGit(t, "name: demo\n")
	mgr := NewManager(base)
	if _, err := mgr.Install("u", "", "0.9.0"); err != nil {
		t.Fatal(err)
	}

	if err := mgr.SetEnabled("demo", true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	list, _ := mgr.List()
	if len(list) != 1 || !list[0].Enabled {
		t.Fatalf("List after enable: %+v", list)
	}

	if err := mgr.SetEnabled("missing", true); err == nil {
		t.Fatal("SetEnabled on missing plugin should error")
	}

	if err := mgr.Remove("demo"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(pluginDir(base, "demo")); !os.IsNotExist(err) {
		t.Fatal("plugin dir not removed")
	}
	list, _ = mgr.List()
	if len(list) != 0 {
		t.Fatalf("registry not cleared after remove: %+v", list)
	}
}
