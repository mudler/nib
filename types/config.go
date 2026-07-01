package types

import (
	"bytes"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
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

// CompactionConfig controls conversation compaction: summarizing older turns
// into a single summary message while keeping recent turns verbatim.
type CompactionConfig struct {
	// Disabled turns OFF automatic compaction. Zero value (false) = auto ON.
	Disabled bool `yaml:"disabled"`
	// MaxContextTokens is the model context window used to compute the trigger.
	// 0 → default 128000.
	MaxContextTokens int `yaml:"max_context_tokens"`
	// Threshold is the fraction of MaxContextTokens at which auto-compaction
	// fires. 0 → default 0.8.
	Threshold float64 `yaml:"threshold"`
	// KeepRecent is the number of trailing messages kept verbatim. 0 → default 8.
	KeepRecent int `yaml:"keep_recent"`
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
	// Metadata overlays the global Config.Metadata for this agent type
	// (per-key: agent keys win, global-only keys are inherited).
	Metadata map[string]string `yaml:"metadata,omitempty"`
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
	Model    string `yaml:"model"`
	APIKey   string `yaml:"api_key"`
	BaseURL  string `yaml:"base_url"`
	LogLevel string `yaml:"log_level"`
	Prompt   string `yaml:"prompt"`
	// Metadata is a per-request metadata object attached verbatim to every
	// chat-completion request (the OpenAI "metadata" field). Backends such as
	// LocalAI use it for per-request flags, e.g. {"enable_thinking": "false"}
	// to disable reasoning. Applied to the main session and inherited by
	// sub-agents (see AgentTypeConfig.Metadata for per-agent overrides).
	Metadata map[string]string `yaml:"metadata,omitempty"`
	// ReasoningEffort sets the OpenAI "reasoning_effort" on every request
	// ("none"/"low"/"medium"/"high"). Unlike Metadata.enable_thinking, this binds
	// even when the model's chat template has no enable_thinking toggle (e.g.
	// LFM2.5), so it's the reliable way to disable a reasoning model's thinking
	// ("none"). Empty leaves the field unset.
	ReasoningEffort string               `yaml:"reasoning_effort,omitempty"`
	MCPServers      map[string]MCPServer `yaml:"mcp_servers"`
	AgentOptions    AgentOptions         `yaml:"agent_options"`
	Compaction      CompactionConfig     `yaml:"compaction"`
	Agents          []AgentTypeConfig    `yaml:"agents"`

	PromptFragments []string `yaml:"prompt_fragments"`
	Skills          []Skill  `yaml:"skills"`

	Commands []CommandConfig `yaml:"commands"`

	Hooks []HookConfig `yaml:"hooks"`

	// ApprovalMode controls tool-call gating:
	//   "" / "prompt"  ask the user, but auto-approve read-only calls
	//   "strict"       ask the user for every call (no read-only auto-approval)
	//   "allowlist"    auto-approve only the tools in AllowedTools, prompt the rest
	//   "auto"         approve every tool call
	ApprovalMode string `yaml:"approval_mode"`
	// AllowedTools are tool names pre-approved without prompting (always honored;
	// the basis of "allowlist" mode).
	AllowedTools []string `yaml:"allowed_tools"`
	// BuiltinTools, if non-empty, restricts which built-in tools and
	// self-config tools are exposed to the model (by name) — an allowlist.
	// Empty means all of them. Trims the prompt for small local models;
	// independent of AllowedTools (which gates approval). Never restricts
	// tools from user-configured MCP servers (mcp_servers:) — those are
	// always exposed, since restricting them would defeat the point of
	// configuring the server. See chat.Session's MCP tool filter.
	BuiltinTools []string `yaml:"builtin_tools,omitempty"`
	// ReadOnlyCommands extends the built-in set of bash commands treated as
	// read-only (auto-approved in the default "prompt" mode). An entry with a
	// space is a command+subcommand pair (e.g. "terraform plan"); otherwise it
	// matches that command at any arguments. User entries are merged with, not
	// replacing, the built-in set.
	ReadOnlyCommands []string `yaml:"read_only_commands"`
	// TraceDir, when non-empty, enables session tracing: each LLM call's raw
	// request/response is appended to <TraceDir>/trace.ndjson. Set at runtime
	// from the --trace-dir flag or NIB_TRACE_DIR env, not from the YAML config.
	TraceDir string `yaml:"-"`
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

	if found := detectContextFiles(currentDirectory); len(found) > 0 {
		b.WriteString("\n\nThe following project instruction file(s) were found in the working directory: ")
		b.WriteString(strings.Join(found, ", "))
		b.WriteString(".\nRead ")
		if len(found) == 1 {
			b.WriteString("it")
		} else {
			b.WriteString("them")
		}
		b.WriteString(" before acting on this repository and follow the instructions ")
		if len(found) == 1 {
			b.WriteString("it contains")
		} else {
			b.WriteString("they contain")
		}
		b.WriteString(".")
	}

	b.WriteString("\n\nYou can register additional MCP servers from the command line: ")
	b.WriteString("`nib mcp add <name> -- <command> [args...]` for a local server, or ")
	b.WriteString("`nib mcp add <name> --url <url> [--transport http|sse]` for a remote one; ")
	b.WriteString("`nib mcp list` and `nib mcp test <name>` show and verify them. ")
	b.WriteString("Servers added this way become available on the next nib session.")

	return b.String()
}

// contextFileNames lists the project instruction files nib looks for in the
// working directory. Their presence is surfaced in the system prompt so the
// agent reads them before acting on the repository.
var contextFileNames = []string{"AGENTS.md", "CLAUDE.md", "NIB.md", "GEMINI.md"}

// detectContextFiles returns the names of known project instruction files that
// exist as regular files in dir, preserving contextFileNames order.
func detectContextFiles(dir string) []string {
	if dir == "" {
		return nil
	}
	var found []string
	for _, name := range contextFileNames {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			found = append(found, name)
		}
	}
	return found
}

type MCPServer struct {
	Command   string            `yaml:"command,omitempty"`
	Args      []string          `yaml:"args,omitempty"`
	Env       map[string]string `yaml:"env,omitempty"`
	URL       string            `yaml:"url,omitempty"`       // remote: presence selects an HTTP/SSE transport
	Transport string            `yaml:"transport,omitempty"` // remote transport: "http" (default) or "sse"

	BearerToken string            `yaml:"token,omitempty"`   // remote only: sent as "Authorization: Bearer <token>"
	Headers     map[string]string `yaml:"headers,omitempty"` // remote only: custom HTTP headers
}
