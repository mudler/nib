package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// loadClaudeManifest maps a Claude Code plugin's .claude-plugin/ layout into a
// wiz Manifest: plugin.json meta, skills/, commands/, agents/, hooks/hooks.json,
// and .mcp.json. Bodies are loaded inline; unmappable items are skipped with a
// stderr warning.
func loadClaudeManifest(dir string) (Manifest, error) {
	metaData, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	if err != nil {
		return Manifest{}, fmt.Errorf("claude plugin: reading plugin.json: %w", err)
	}
	var meta struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return Manifest{}, fmt.Errorf("claude plugin: parsing plugin.json: %w", err)
	}

	return Manifest{
		Name:        meta.Name,
		Version:     meta.Version,
		Description: meta.Description,
		MCPServers:  loadClaudeMCP(dir),
		Agents:      loadClaudeAgents(dir),
		Skills:      loadClaudeSkills(dir),
		Commands:    loadClaudeCommands(dir),
		Hooks:       loadClaudeHooks(dir),
	}, nil
}
