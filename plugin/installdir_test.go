package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

// InstallDir installs a prepared plugin directory DISABLED, taking the plugin
// name from its manifest.
func TestPluginInstallDir_LandsDisabled(t *testing.T) {
	base := t.TempDir()
	src := t.TempDir()
	// Minimal valid native nib plugin manifest (see plugin/detect.go:
	// NativeManifestFile == "nib-plugin.yaml"; Validate only requires name).
	writeFile(t, filepath.Join(src, "nib-plugin.yaml"),
		"name: myplug\ndescription: test plugin\n")

	mgr := NewManager(base)
	m, err := mgr.InstallDir(src, "v0.0.0")
	if err != nil {
		t.Fatalf("InstallDir: %v", err)
	}
	if m.Name != "myplug" {
		t.Fatalf("manifest name = %q", m.Name)
	}
	if _, err := os.Stat(filepath.Join(PluginsDir(base), "myplug")); err != nil {
		t.Fatalf("plugin not placed: %v", err)
	}
	reg, _ := LoadRegistry(base)
	if e := reg.Find("myplug"); e == nil || e.Enabled {
		t.Fatalf("expected registered & disabled, got %+v", e)
	}
}
