package manage

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mudler/nib/types"

	"gopkg.in/yaml.v3"
)

// MCPServerInfo is a configured MCP server in tool-facing form.
type MCPServerInfo struct {
	Name    string
	Command string
	Args    []string
}

// userConfigServers reads only the user config file's mcp_servers map (not the
// merged effective set), so writes never persist plugin-contributed servers.
func userConfigServers(path string) (map[string]types.MCPServer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]types.MCPServer{}, nil
		}
		return nil, err
	}
	var doc struct {
		MCPServers map[string]types.MCPServer `yaml:"mcp_servers"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.MCPServers == nil {
		doc.MCPServers = map[string]types.MCPServer{}
	}
	return doc.MCPServers, nil
}

// writeUserConfigServers rewrites only the mcp_servers key, preserving every
// other key (including unknown ones) by round-tripping through a generic map.
func writeUserConfigServers(path string, servers map[string]types.MCPServer) error {
	root := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, &root); err != nil {
			return err
		}
	}
	if root == nil {
		root = map[string]any{}
	}
	if len(servers) == 0 {
		delete(root, "mcp_servers")
	} else {
		root["mcp_servers"] = servers
	}
	out, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// ListMCPServers returns the MCP servers configured in the user config file,
// sorted by name. It reads the writable user file (the authoritative source for
// add/remove), not the merged effective config.
func (c *Configurator) ListMCPServers() ([]MCPServerInfo, error) {
	servers, err := userConfigServers(c.configPath)
	if err != nil {
		return nil, err
	}
	out := make([]MCPServerInfo, 0, len(servers))
	for name, s := range servers {
		out = append(out, MCPServerInfo{Name: name, Command: s.Command, Args: s.Args})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// AddMCPServer persists an MCP server to the user config file.
func (c *Configurator) AddMCPServer(name, command string, args []string, env map[string]string) error {
	if name == "" || command == "" {
		return fmt.Errorf("name and command are required")
	}
	servers, err := userConfigServers(c.configPath)
	if err != nil {
		return err
	}
	servers[name] = types.MCPServer{Command: command, Args: args, Env: env}
	return writeUserConfigServers(c.configPath, servers)
}

// RemoveMCPServer deletes an MCP server from the user config file.
func (c *Configurator) RemoveMCPServer(name string) error {
	servers, err := userConfigServers(c.configPath)
	if err != nil {
		return err
	}
	if _, ok := servers[name]; !ok {
		return fmt.Errorf("mcp server %q not configured in %s", name, c.configPath)
	}
	delete(servers, name)
	return writeUserConfigServers(c.configPath, servers)
}
