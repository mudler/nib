package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

// Installing from a plain local directory (not a git repo) should copy it.
func TestInstallFromLocalDir(t *testing.T) {
	base := t.TempDir()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "nib-plugin.yaml"), "name: localdemo\nversion: 1.0.0\n")
	writeFile(t, filepath.Join(src, "hooks", "h.sh"), "#!/bin/sh\necho hi\n")

	mgr := NewManager(base)
	m, err := mgr.Install(src, "", "0.9.0") // no git repo, no ref
	if err != nil {
		t.Fatalf("Install from local dir: %v", err)
	}
	if m.Name != "localdemo" {
		t.Fatalf("name = %q", m.Name)
	}
	// files were copied to plugins/<name>
	if _, err := os.Stat(filepath.Join(pluginDir(base, "localdemo"), "hooks", "h.sh")); err != nil {
		t.Fatalf("nested file not copied: %v", err)
	}
}
