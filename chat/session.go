package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/mudler/wiz/hooks"
	"github.com/mudler/wiz/types"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/cogito"
	"github.com/mudler/cogito/clients"
	"github.com/mudler/xlog"
	openai "github.com/sashabaranov/go-openai"
)

// Session represents a chat session with the AI assistant
type Session struct {
	ctx           context.Context
	turnMu        sync.Mutex
	turnCancel    context.CancelFunc
	llm           cogito.LLM
	clients       []*mcp.ClientSession
	fragment      cogito.Fragment
	messages      []openai.ChatCompletionMessage
	callbacks     Callbacks
	systemPrompt  string
	skills        []types.Skill
	cogitoOptions types.AgentOptions
	allowedTools  map[string]bool // Tools that don't need approval this session
	hooks         *hooks.Dispatcher

	agentManager *cogito.AgentManager
	agentDefs    []cogito.AgentDefinition
	llmModel     string
	apiKey       string
	baseURL      string
}

// toCogitoDefinitions converts wiz agent-type config into cogito definitions.
func toCogitoDefinitions(cfgs []types.AgentTypeConfig) []cogito.AgentDefinition {
	defs := make([]cogito.AgentDefinition, 0, len(cfgs))
	for _, t := range cfgs {
		defs = append(defs, cogito.AgentDefinition{
			Name:         t.Name,
			Description:  t.Description,
			SystemPrompt: t.SystemPrompt,
			Tools:        t.Tools,
			Model:        t.Model,
			Temperature:  t.Temperature,
			Iterations:   t.Iterations,
			MaxAttempts:  t.MaxAttempts,
			MaxRetries:   t.MaxRetries,
		})
	}
	return defs
}

// CommandTransport creates a new transport for a command
func CommandTransport(cmd string, args []string, env ...string) mcp.Transport {
	command := exec.Command(cmd, args...)
	command.Env = os.Environ()
	command.Env = append(command.Env, env...)

	transport := &mcp.CommandTransport{Command: command}
	return transport
}

// NewSession creates a new chat session
func NewSession(ctx context.Context, cfg types.Config, callbacks Callbacks, transports ...mcp.Transport) (*Session, error) {
	llm := clients.NewOpenAILLM(cfg.Model, cfg.APIKey, cfg.BaseURL)

	agentManager := cogito.NewAgentManager()

	client := mcp.NewClient(&mcp.Implementation{Name: "aish", Version: "v1.0.0"}, nil)
	clients := []*mcp.ClientSession{}

	for _, transport := range transports {
		session, err := client.Connect(ctx, transport, nil)
		if err != nil {
			// A single MCP server that fails to start (e.g. a plugin whose
			// binary isn't on PATH) must not prevent the whole session from
			// coming up. Skip it and continue with the rest.
			xlog.Warn("Skipping MCP server that failed to connect", "error", err)
			continue
		}
		clients = append(clients, session)
	}

	s := &Session{
		ctx:           ctx,
		llm:           llm,
		clients:       clients,
		fragment:      cogito.NewEmptyFragment(),
		messages:      []openai.ChatCompletionMessage{},
		callbacks:     callbacks,
		systemPrompt:  cfg.GetPrompt(),
		skills:        cfg.Skills,
		cogitoOptions: cfg.AgentOptions,
		allowedTools:  make(map[string]bool),
		hooks:         hooks.New(cfg.Hooks),
		agentManager:  agentManager,
		agentDefs:     toCogitoDefinitions(cfg.Agents),
		llmModel:      cfg.Model,
		apiKey:        cfg.APIKey,
		baseURL:       cfg.BaseURL,
	}
	s.hooks.Fire(ctx, hooks.EventSessionStart, "", map[string]any{"event": "SessionStart"})
	return s, nil
}

// LoadSkill appends a named skill's instructions to the session system prompt
// (eager load via /skill), so subsequent turns include it without a load_skill
// tool call. Returns a short notice for the transcript.
func (s *Session) LoadSkill(name string) (string, error) {
	for _, sk := range s.skills {
		if sk.Name == name {
			s.systemPrompt += "\n\n# Skill: " + sk.Name + "\n" + sk.Instructions
			return fmt.Sprintf("Loaded skill %q: %s", sk.Name, sk.Description), nil
		}
	}
	return "", fmt.Errorf("unknown skill %q", name)
}

// decideToolCall resolves a tool-call request: PreToolUse hooks first (a hook
// may block/approve/adjust), then the session allow-list, then the user gate.
func (s *Session) decideToolCall(req ToolCallRequest) cogito.ToolCallDecision {
	if s.hooks != nil {
		decisions := s.hooks.Fire(s.ctx, hooks.EventPreToolUse, req.Name, map[string]any{
			"event":     "PreToolUse",
			"tool":      req.Name,
			"arguments": req.Arguments,
			"reasoning": req.Reasoning,
			"agent_id":  req.AgentID,
		})
		if td := hooks.CombineToolDecisions(decisions); td.Decided {
			adjustment := td.Adjustment
			if !td.Approve && adjustment == "" {
				adjustment = td.Reason
			}
			return cogito.ToolCallDecision{Approved: td.Approve, Adjustment: adjustment}
		}
	}

	if s.allowedTools[req.Name] {
		return cogito.ToolCallDecision{Approved: true}
	}
	if s.callbacks.OnToolCall == nil {
		return cogito.ToolCallDecision{Approved: true}
	}
	resp := s.callbacks.OnToolCall(req)
	if resp.AlwaysAllow && resp.Approved {
		s.allowedTools[req.Name] = true
	}
	return cogito.ToolCallDecision{Approved: resp.Approved, Adjustment: resp.Adjustment}
}

// ToolCallDenied reports whether the given tool call would be denied (used to
// verify PreToolUse hook gating end-to-end).
func (s *Session) ToolCallDenied(req ToolCallRequest) bool {
	return !s.decideToolCall(req).Approved
}

// AgentManager exposes the sub-agent registry so the UI can list and detach agents.
func (s *Session) AgentManager() *cogito.AgentManager {
	return s.agentManager
}

// emitAgentEvent maps a cogito sub-agent state into a chat.AgentEvent and
// forwards it to the registered OnAgentEvent callback (if any). It is shared by
// the spawn (Status=running) and completion callbacks so the mapping lives in
// one place. s.callbacks.OnAgentEvent is set once in NewSession and never
// reassigned, so reading it from cogito's spawn goroutines is safe.
func (s *Session) emitAgentEvent(a *cogito.AgentState) {
	if s.callbacks.OnAgentEvent != nil {
		s.callbacks.OnAgentEvent(AgentEvent{
			ID:     a.ID,
			Type:   a.Type,
			Task:   a.Task,
			Status: AgentStatus(a.Status),
			Result: a.Result,
			Err:    a.Error,
		})
	}
	if s.hooks != nil {
		s.hooks.Fire(s.ctx, hooks.EventAgentEvent, string(a.Status), map[string]any{
			"event":  "AgentEvent",
			"id":     a.ID,
			"type":   a.Type,
			"status": string(a.Status),
		})
	}
}

func (s *Session) ClearHistory() {
	s.messages = []openai.ChatCompletionMessage{}
	s.fragment = cogito.NewEmptyFragment()
}

// beginTurn starts a per-turn cancellable context derived from the session
// context and stores its cancel func so Interrupt can cancel just this turn.
func (s *Session) beginTurn() context.Context {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	ctx, cancel := context.WithCancel(s.ctx)
	s.turnCancel = cancel
	return ctx
}

// endTurn releases the current turn context.
func (s *Session) endTurn() {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	if s.turnCancel != nil {
		s.turnCancel()
		s.turnCancel = nil
	}
}

// Interrupt cancels the in-flight turn (and any sub-agents spawned within it),
// leaving the session alive. Safe to call when no turn is running.
func (s *Session) Interrupt() {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	if s.turnCancel != nil {
		s.turnCancel()
	}
}

// SendMessage sends a message to the assistant and processes the response
func (s *Session) SendMessage(text string) (string, error) {
	if s.hooks != nil {
		s.hooks.Fire(s.ctx, hooks.EventUserPromptSubmit, "", map[string]any{"event": "UserPromptSubmit", "prompt": text})
	}
	turnCtx := s.beginTurn()
	defer s.endTurn()
	if s.systemPrompt != "" {
		s.fragment = s.fragment.AddMessage("system", s.systemPrompt)
	}
	s.fragment = s.fragment.AddMessage("user", text)
	s.messages = append(s.messages, openai.ChatCompletionMessage{
		Role:    "user",
		Content: text,
	})

	// Build cogito options from config
	cogitoOpts := []cogito.Option{
		cogito.WithContext(turnCtx),
		cogito.WithIterations(s.cogitoOptions.Iterations),
		cogito.WithMaxAttempts(s.cogitoOptions.MaxAttempts),
		cogito.WithMaxRetries(s.cogitoOptions.MaxRetries),
		cogito.WithStatusCallback(func(status string) {
			if s.callbacks.OnStatus != nil {
				s.callbacks.OnStatus(status)
			}
		}),
		cogito.WithReasoningCallback(func(reasoning string) {
			if s.callbacks.OnReasoning != nil {
				s.callbacks.OnReasoning(reasoning)
			}
		}),
		cogito.WithMCPs(s.clients...),
		cogito.WithToolCallBack(func(tool *cogito.ToolChoice, state *cogito.SessionState) cogito.ToolCallDecision {
			args, err := json.Marshal(tool.Arguments)
			if err != nil {
				return cogito.ToolCallDecision{Approved: false}
			}
			return s.decideToolCall(ToolCallRequest{
				Name:      tool.Name,
				Arguments: string(args),
				Reasoning: tool.Reasoning,
				AgentID:   state.AgentID,
			})
		}),
		cogito.WithToolCallResultCallback(func(status cogito.ToolStatus) {
			if s.hooks != nil {
				s.hooks.Fire(s.ctx, hooks.EventPostToolUse, status.Name, map[string]any{
					"event":  "PostToolUse",
					"tool":   status.Name,
					"result": status.Result,
				})
			}
		}),
	}

	cogitoOpts = append(cogitoOpts,
		// Disable cogito's sink-state "reply" tool so ExecuteTools is the whole
		// turn: when the LLM stops calling tools it records its text reply as the
		// final answer (read via LastMessage below). This matches cogito's own
		// examples (examples/chat, examples/sub-agents) and avoids a redundant
		// follow-up Ask that returns empty on many models.
		cogito.DisableSinkState,
		cogito.EnableAgentSpawning,
		cogito.WithAgentManager(s.agentManager),
		cogito.WithAgentDefinitions(s.agentDefs...),
		cogito.WithAgentLLMFactory(func(model string, temperature float32) cogito.LLM {
			return clients.NewOpenAILLMWithOptions(model, s.apiKey, s.baseURL, clients.OpenAIOptions{Temperature: temperature})
		}),
		cogito.WithAgentSpawnCallback(func(a *cogito.AgentState) {
			s.emitAgentEvent(a)
		}),
		cogito.WithAgentCompletionCallback(func(a *cogito.AgentState) {
			s.emitAgentEvent(a)
		}),
		cogito.WithTools(askUserToolDefinition(func(req AskRequest) string {
			if s.callbacks.OnAskUser != nil {
				return s.callbacks.OnAskUser(req)
			}
			return ""
		})),
	)

	// Add ForceReasoning only if enabled in config
	if s.cogitoOptions.ForceReasoning {
		cogitoOpts = append(cogitoOpts, cogito.WithForceReasoning())
	}

	// Run the agent loop. With sink-state disabled, ExecuteTools runs the whole
	// turn and leaves the final natural-language answer as the last message.
	var err error
	s.fragment, err = cogito.ExecuteTools(s.llm, s.fragment, cogitoOpts...)
	if err != nil && !errors.Is(err, cogito.ErrNoToolSelected) {
		if s.callbacks.OnError != nil {
			s.callbacks.OnError(err)
		}
		return "", err
	}

	response := s.fragment.LastMessage().Content
	s.messages = append(s.messages, openai.ChatCompletionMessage{
		Role:    "assistant",
		Content: response,
	})

	if s.callbacks.OnResponse != nil {
		s.callbacks.OnResponse(response)
	}

	if s.hooks != nil {
		s.hooks.Fire(s.ctx, hooks.EventStop, "", map[string]any{"event": "Stop"})
	}

	return response, nil
}

// GetMessages returns all messages in the conversation
func (s *Session) GetMessages() []Message {
	messages := []Message{}
	for _, msg := range s.messages {
		messages = append(messages, Message{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}
	return messages
}

// Close closes the session and cleans up resources
func (s *Session) Close() error {
	for _, client := range s.clients {
		if err := client.Close(); err != nil {
			return err
		}
	}
	return nil
}
