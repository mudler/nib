package claudeplugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree creates files under root. A key ending in "*" is written
// executable (the star is dropped).
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		mode := os.FileMode(0o644)
		if n := len(name); n > 0 && name[n-1] == '*' {
			name, mode = name[:n-1], 0o755
		}
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
}

func hasSkip(p *Plugin, component, substr string) bool {
	for _, s := range p.Skipped {
		if s.Component == component && (strings.Contains(s.Reason, substr) || strings.Contains(s.Name, substr)) {
			return true
		}
	}
	return false
}
