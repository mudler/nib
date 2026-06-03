package types

import (
	"bytes"
	"fmt"
	"os"
	"os/user"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
)

// AgentOptions holds configuration for the cogito ExecuteTools function
type AgentOptions struct {
	Iterations     int  `yaml:"iterations"`
	MaxAttempts    int  `yaml:"max_attempts"`
	MaxRetries     int  `yaml:"max_retries"`
	ForceReasoning bool `yaml:"force_reasoning"`
}

// AgentTypeConfig is a wiz-facing sub-agent type. It maps 1:1 to a
// cogito.AgentDefinition. Zero-valued numeric fields mean "inherit".
type AgentTypeConfig struct {
	Name         string   `yaml:"name"`
	Description  string   `yaml:"description"`
	SystemPrompt string   `yaml:"system_prompt"`
	Tools        []string `yaml:"tools"`
	Model        string   `yaml:"model"`
	Temperature  float32  `yaml:"temperature"`
	Iterations   int      `yaml:"iterations"`
	MaxAttempts  int      `yaml:"max_attempts"`
	MaxRetries   int      `yaml:"max_retries"`
}

// Skill is a named, on-demand instruction set. Its Description is listed in the
// system prompt; the agent calls the load_skill tool to read Instructions.
type Skill struct {
	Name         string   `yaml:"name"`
	Description  string   `yaml:"description"`
	Instructions string   `yaml:"instructions"` // resolved body (inline, or loaded from a plugin file)
	Tools        []string `yaml:"tools,omitempty"`
	Dir          string   `yaml:"-"` // absolute on-disk dir for bundled files; runtime-only, never serialized
}

// CommandConfig is a named slash command: a prompt template (text/template with
// {{.Args}} and {{.CurrentDirectory}}) optionally routed through a sub-agent.
type CommandConfig struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Prompt      string `yaml:"prompt"`
	Agent       string `yaml:"agent,omitempty"`
}

// HookConfig is a shell command bound to a lifecycle event. Matcher (optional)
// is matched against the tool name for PreToolUse/PostToolUse. Dir is the
// plugin root (set during merge); it is the command's working directory and is
// exported as ${NIB_PLUGIN_ROOT}/${CLAUDE_PLUGIN_ROOT}.
type HookConfig struct {
	Event   string `yaml:"event"`
	Matcher string `yaml:"matcher,omitempty"`
	Command string `yaml:"command"`
	Dir     string `yaml:"-"` // plugin root; set during merge, not parsed
}

// Config holds configuration for creating a new session
type Config struct {
	Model        string               `yaml:"model"`
	APIKey       string               `yaml:"api_key"`
	BaseURL      string               `yaml:"base_url"`
	LogLevel     string               `yaml:"log_level"`
	Prompt       string               `yaml:"prompt"`
	MCPServers   map[string]MCPServer `yaml:"mcp_servers"`
	AgentOptions AgentOptions         `yaml:"agent_options"`
	Agents       []AgentTypeConfig    `yaml:"agents"`

	PromptFragments []string `yaml:"prompt_fragments"`
	Skills          []Skill  `yaml:"skills"`

	Commands []CommandConfig `yaml:"commands"`

	Hooks []HookConfig `yaml:"hooks"`

	// ApprovalMode controls tool-call gating: "" / "prompt" (ask the user),
	// "auto" (approve every tool call), or "allowlist" (auto-approve only the
	// tools in AllowedTools and prompt for the rest).
	ApprovalMode string `yaml:"approval_mode"`
	// AllowedTools are tool names pre-approved without prompting (always honored;
	// the basis of "allowlist" mode).
	AllowedTools []string `yaml:"allowed_tools"`
}

func (c *Config) GetPrompt() string {
	tmpl, err := template.New("").Funcs(sprig.FuncMap()).Parse(c.Prompt)
	if err != nil {
		return ""
	}

	data := bytes.NewBuffer([]byte{})

	currentDirectory, err := os.Getwd()
	if err != nil {
		currentDirectory = ""
	}
	currentUser, err := user.Current()
	if err != nil {
		currentUser = &user.User{}
	}

	if err := tmpl.Execute(data, struct {
		Config           Config
		CurrentDirectory string
		CurrentUser      string
	}{
		Config:           *c,
		CurrentDirectory: currentDirectory,
		CurrentUser:      currentUser.Username,
	}); err != nil {
		return ""
	}

	var b strings.Builder
	b.WriteString(data.String())

	if len(c.Skills) > 0 {
		b.WriteString("\n\nAvailable skills — call the load_skill tool with the skill name to read its full instructions before acting on a matching task:\n")
		for _, s := range c.Skills {
			fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Description)
		}
	}

	for _, f := range c.PromptFragments {
		if strings.TrimSpace(f) == "" {
			continue
		}
		b.WriteString("\n\n")
		b.WriteString(f)
	}

	return b.String()
}

type MCPServer struct {
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env"`
}
