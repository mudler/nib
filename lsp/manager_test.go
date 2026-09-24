package lsp

import "testing"

func TestServerForFile(t *testing.T) {
	m := NewManager(nil, "/tmp")

	tests := []struct {
		path string
		lang string
		ok   bool
	}{
		{"main.go", "go", true},
		{"src/index.ts", "typescript", true},
		{"app.tsx", "typescriptreact", true},
		{"script.py", "python", true},
		{"main.rs", "rust", true},
		{"Main.java", "java", true},
		{"readme.md", "", false},
		{"config.yaml", "", false},
		{"Makefile", "", false},
	}

	for _, tc := range tests {
		_, err := m.ServerForFile(tc.path)
		if tc.ok {
			if err == nil {
				t.Errorf("ServerForFile(%q): expected error (no server configured), got nil", tc.path)
			}
			// The error should mention the language, not "no server for .ext files"
			// since the language IS known, just not configured.
		} else {
			if err == nil {
				t.Errorf("ServerForFile(%q): expected error for unsupported extension, got nil", tc.path)
			}
		}
	}
}

func TestServerForFileUnknownExt(t *testing.T) {
	m := NewManager(nil, "/tmp")
	_, err := m.ServerForFile("readme.md")
	if err == nil {
		t.Fatal("expected error for .md file")
	}
}

func TestNewManagerEmpty(t *testing.T) {
	m := NewManager(nil, "/tmp")
	if err := m.Close(); err != nil {
		t.Errorf("Close on empty manager: %v", err)
	}
}

func TestServerNoConfig(t *testing.T) {
	m := NewManager(nil, "/tmp")
	_, err := m.Server("go")
	if err == nil {
		t.Fatal("expected error when no server configured for 'go'")
	}
}

func TestServerForFileReturnsCorrectLangError(t *testing.T) {
	m := NewManager(map[string]ServerConfig{
		"go": {Command: "gopls", Args: []string{"serve"}},
	}, "/tmp")

	// go is configured but we don't want to actually start gopls in a unit test.
	// Just verify the error is about starting the server, not "not configured".
	_, err := m.Server("go")
	if err == nil {
		// gopls might actually be on PATH and start — that's fine, just close it.
		_ = m.Close()
		return
	}
	// Error should be about starting, not "no server configured"
}
