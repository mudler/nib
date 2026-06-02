package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/nib/types"
)

// claudeSupportedEvents are the hook events wiz maps from Claude (the names
// already match wiz's events). Claude-only events are skipped with a warning.
var claudeSupportedEvents = map[string]bool{
	"SessionStart":     true,
	"UserPromptSubmit": true,
	"PreToolUse":       true,
	"PostToolUse":      true,
	"Stop":             true,
}

// loadClaudeHooks flattens hooks/hooks.json into wiz HookConfigs, keeping only
// supported events.
func loadClaudeHooks(root string) []types.HookConfig {
	data, err := os.ReadFile(filepath.Join(root, "hooks", "hooks.json"))
	if err != nil {
		return nil
	}
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		fmt.Fprintf(os.Stderr, "wiz: claude hooks.json parse error: %v\n", err)
		return nil
	}
	var out []types.HookConfig
	for event, groups := range doc.Hooks {
		if !claudeSupportedEvents[event] {
			fmt.Fprintf(os.Stderr, "wiz: skipping unsupported claude hook event %q\n", event)
			continue
		}
		for _, g := range groups {
			for _, h := range g.Hooks {
				if h.Type != "" && h.Type != "command" {
					continue
				}
				out = append(out, types.HookConfig{Event: event, Matcher: g.Matcher, Command: h.Command})
			}
		}
	}
	return out
}

// loadClaudeMCP reads .mcp.json (mcpServers) into wiz MCP server configs.
func loadClaudeMCP(root string) map[string]types.MCPServer {
	data, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		return nil
	}
	var doc struct {
		MCPServers map[string]types.MCPServer `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		fmt.Fprintf(os.Stderr, "wiz: claude .mcp.json parse error: %v\n", err)
		return nil
	}
	return doc.MCPServers
}
