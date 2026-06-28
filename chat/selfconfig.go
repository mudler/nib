package chat

import (
	"fmt"
	"strings"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/manage"
	"github.com/mudler/nib/types"
)

// toolDef pairs a runnable tool with its name so callers can register and test
// it. The cogito definition is built in selfConfigToolDefs.
type toolDef struct {
	name string
	tool cogito.Tool[map[string]any]
	def  cogito.ToolDefinitionInterface
}

// noArgs is the empty JSON-schema shape for parameterless tools.
type noArgs struct{}

func argStrSlice(args map[string]any, key string) []string {
	switch v := args[key].(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	}
	return nil
}

// argEnvMap parses an "env" argument given as KEY=VALUE strings into a map.
// cogito's tool-schema generator rejects map-typed argument fields, so env is
// modeled as a []string on the wire and parsed back into a map here.
func argEnvMap(args map[string]any, key string) map[string]string {
	pairs := argStrSlice(args, key)
	if len(pairs) == 0 {
		return nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			continue
		}
		out[k] = v
	}
	return out
}

// fnTool adapts a func to cogito.Tool[map[string]any].
type fnTool struct {
	run func(args map[string]any) (string, error)
}

func (t *fnTool) Run(args map[string]any) (string, any, error) {
	out, err := t.run(args)
	return out, nil, err
}

// makeTool wraps a run func as a named toolDef with its cogito definition.
func makeTool(name, desc string, schema any, run func(args map[string]any) (string, error)) toolDef {
	ft := &fnTool{run: run}
	return toolDef{
		name: name,
		tool: ft,
		def:  cogito.NewToolDefinition[map[string]any](ft, schema, name, desc),
	}
}

type installPluginArgs struct {
	URL string `json:"url" jsonschema:"git URL or local path of the plugin to install"`
	Ref string `json:"ref,omitempty" jsonschema:"optional git ref (branch, tag, or commit)"`
}
type nameArgs struct {
	Name string `json:"name" jsonschema:"the name of the target"`
}
type generateSkillArgs struct {
	Name         string `json:"name" jsonschema:"short, single-word skill name (no slashes)"`
	Description  string `json:"description" jsonschema:"one-line summary shown in the skill list"`
	Instructions string `json:"instructions" jsonschema:"the full skill instructions the agent will follow"`
}
type addMCPServerArgs struct {
	Name      string   `json:"name" jsonschema:"unique name for the MCP server"`
	Command   string   `json:"command,omitempty" jsonschema:"executable to launch a local (stdio) MCP server"`
	Args      []string `json:"args,omitempty" jsonschema:"command arguments"`
	Env       []string `json:"env,omitempty" jsonschema:"environment variables, each as a KEY=VALUE string"`
	URL       string   `json:"url,omitempty" jsonschema:"base URL for a remote MCP server (use instead of command)"`
	Transport string   `json:"transport,omitempty" jsonschema:"remote transport: http (default) or sse"`
}

// selfConfigToolDefs builds the ten self-configuration tools. reload is called
// after any mutating op to mark the session for re-wiring on the next turn.
func selfConfigToolDefs(c *manage.Configurator, reload func()) []toolDef {
	return []toolDef{
		makeTool("list_plugins",
			"List installed plugins with their enabled state, source URL, and ref.",
			noArgs{}, func(args map[string]any) (string, error) {
				ps, err := c.ListPlugins()
				if err != nil {
					return "", err
				}
				if len(ps) == 0 {
					return "No plugins installed.", nil
				}
				var b strings.Builder
				for _, p := range ps {
					state := "disabled"
					if p.Enabled {
						state = "enabled"
					}
					fmt.Fprintf(&b, "- %s [%s] %s %s\n", p.Name, state, p.Ref, p.SourceURL)
				}
				return b.String(), nil
			}),

		makeTool("install_plugin",
			"Install a plugin from a git URL or local path. It is installed DISABLED; call enable_plugin to activate it.",
			installPluginArgs{}, func(args map[string]any) (string, error) {
				url := argStr(args, "url")
				if url == "" {
					return "url is required", nil
				}
				p, err := c.InstallPlugin(url, argStr(args, "ref"))
				if err != nil {
					return "", err
				}
				reload()
				return fmt.Sprintf("Installed plugin %q (disabled). Call enable_plugin with name %q to activate it.", p.Name, p.Name), nil
			}),

		makeTool("enable_plugin",
			"Enable an installed plugin so its MCP servers, agents, skills, and hooks activate on the next message.",
			nameArgs{}, func(args map[string]any) (string, error) {
				name := argStr(args, "name")
				if err := c.SetPluginEnabled(name, true); err != nil {
					return "", err
				}
				reload()
				return fmt.Sprintf("Enabled plugin %q. Its contributions are active on the next message.", name), nil
			}),

		makeTool("disable_plugin",
			"Disable an installed plugin so its contributions are removed on the next message.",
			nameArgs{}, func(args map[string]any) (string, error) {
				name := argStr(args, "name")
				if err := c.SetPluginEnabled(name, false); err != nil {
					return "", err
				}
				reload()
				return fmt.Sprintf("Disabled plugin %q.", name), nil
			}),

		makeTool("remove_plugin",
			"Permanently delete an installed plugin's files and registry record.",
			nameArgs{}, func(args map[string]any) (string, error) {
				name := argStr(args, "name")
				if err := c.RemovePlugin(name); err != nil {
					return "", err
				}
				reload()
				return fmt.Sprintf("Removed plugin %q.", name), nil
			}),

		makeTool("list_skills",
			"List skills contributed by enabled skill packs, with their pack name.",
			noArgs{}, func(args map[string]any) (string, error) {
				sk, err := c.ListSkills()
				if err != nil {
					return "", err
				}
				if len(sk) == 0 {
					return "No skills available.", nil
				}
				var b strings.Builder
				for _, s := range sk {
					fmt.Fprintf(&b, "- %s (%s): %s\n", s.Name, s.Pack, s.Description)
				}
				return b.String(), nil
			}),

		makeTool("generate_skill",
			"Author a new skill: writes a SKILL.md into the local skill pack and registers it so load_skill can use it on the next message.",
			generateSkillArgs{}, func(args map[string]any) (string, error) {
				info, err := c.GenerateSkill(argStr(args, "name"), argStr(args, "description"), argStr(args, "instructions"))
				if err != nil {
					return "", err
				}
				reload()
				return fmt.Sprintf("Created skill %q in pack %q. Loadable via load_skill on the next message.", info.Name, info.Pack), nil
			}),

		makeTool("list_mcp_servers",
			"List MCP servers configured in the user config file.",
			noArgs{}, func(args map[string]any) (string, error) {
				servers, err := c.ListMCPServers()
				if err != nil {
					return "", err
				}
				if len(servers) == 0 {
					return "No MCP servers configured.", nil
				}
				var b strings.Builder
				for _, s := range servers {
					if s.URL != "" {
						tr := s.Transport
						if tr == "" {
							tr = "http"
						}
						fmt.Fprintf(&b, "- %s: %s %s\n", s.Name, tr, s.URL)
					} else {
						fmt.Fprintf(&b, "- %s: %s %s\n", s.Name, s.Command, strings.Join(s.Args, " "))
					}
				}
				return b.String(), nil
			}),

		makeTool("add_mcp_server",
			"Add an MCP server (local command or remote url) to the user config and connect it on the next message.",
			addMCPServerArgs{}, func(args map[string]any) (string, error) {
				name := argStr(args, "name")
				srv := types.MCPServer{
					Command:   argStr(args, "command"),
					Args:      argStrSlice(args, "args"),
					Env:       argEnvMap(args, "env"),
					URL:       argStr(args, "url"),
					Transport: argStr(args, "transport"),
				}
				if err := c.AddMCPServer(name, srv); err != nil {
					return "", err
				}
				reload()
				return fmt.Sprintf("Added MCP server %q. It connects on the next message.", name), nil
			}),

		makeTool("remove_mcp_server",
			"Remove an MCP server from the user config and disconnect it on the next message.",
			nameArgs{}, func(args map[string]any) (string, error) {
				name := argStr(args, "name")
				if err := c.RemoveMCPServer(name); err != nil {
					return "", err
				}
				reload()
				return fmt.Sprintf("Removed MCP server %q.", name), nil
			}),
	}
}
