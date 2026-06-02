package plugin

import (
	"path/filepath"
	"testing"
)

func TestLoadClaudeManifestFull(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"),
		`{"name": "demo", "version": "1.2.3", "description": "a claude plugin"}`)
	writeFile(t, filepath.Join(dir, "skills", "s1", "SKILL.md"),
		"---\nname: s1\ndescription: skill one\n---\nbody one\n")
	writeFile(t, filepath.Join(dir, ".mcp.json"),
		`{"mcpServers": {"srv": {"command": "mcp-srv", "args": ["serve"]}}}`)

	m, err := loadClaudeManifest(dir)
	if err != nil {
		t.Fatalf("loadClaudeManifest: %v", err)
	}
	if m.Name != "demo" || m.Version != "1.2.3" || m.Description != "a claude plugin" {
		t.Fatalf("meta wrong: %+v", m)
	}
	if len(m.Skills) != 1 || m.Skills[0].Name != "s1" {
		t.Fatalf("skills wrong: %+v", m.Skills)
	}
	if m.MCPServers["srv"].Command != "mcp-srv" {
		t.Fatalf("mcp wrong: %+v", m.MCPServers)
	}
}
