package types

import (
	"bytes"
	"os"
	"os/user"
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

// ReviewerLLMConfig holds configuration for the reviewer LLM (used in plan mode)
type ReviewerLLMConfig struct {
	Model   string `yaml:"model"`
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
	Enabled *bool  `yaml:"enabled"` // If nil, defaults to true when reviewer_llm is configured
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
	ReviewerLLM  *ReviewerLLMConfig   `yaml:"reviewer_llm"`
	Agents       []AgentTypeConfig    `yaml:"agents"`
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

	return data.String()
}

type MCPServer struct {
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env"`
}
