package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"sync"

	"github.com/mudler/nib/config"
	"github.com/mudler/nib/hooks"
	"github.com/mudler/nib/manage"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/plugin"
	"github.com/mudler/nib/types"

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
	mcpClient     *mcp.Client
	cfgClients    map[string]*mcp.ClientSession // config/plugin MCP servers, by name
	cfgServers    map[string]types.MCPServer    // desired set, for diffing
	skillsClient  *mcp.ClientSession            // the load_skill server
	fragment      cogito.Fragment
	messages      []openai.ChatCompletionMessage
	callbacks     Callbacks
	systemPrompt  string
	loadedSkills  string // eager-loaded /skill instructions, re-applied across reloads
	skills        []types.Skill
	cogitoOptions types.AgentOptions
	allowedTools  map[string]bool // Tools that don't need approval this session
	autoApprove   bool            // approval_mode: auto — approve every tool call
	allowAllTurn  bool            // user chose "allow all this turn"; reset each top-level turn
	hooks         *hooks.Dispatcher

	agentManager *cogito.AgentManager
	agentDefs    []cogito.AgentDefinition
	agentModels  map[string]bool // models configured per agent type (for the LLM-model guard)
	agentLogs    *agentLogStore  // per-sub-agent activity log (for the agent_logs tool)
	llmModel     string
	apiKey       string
	baseURL      string

	configurator  *manage.Configurator
	reloadMu      sync.Mutex
	pendingReload bool
}

// AgentLog returns the captured activity log for a sub-agent (for the
// agent_logs tool / UI inspection).
func (s *Session) AgentLog(agentID string) string {
	if s.agentLogs == nil {
		return ""
	}
	return s.agentLogs.dump(agentID)
}

// resolveAgentModel picks the model for a sub-agent: the requested model when
// wiz actually serves it (the main model, or one configured for an agent type),
// otherwise the main model. This honors per-agent model overrides from config
// while ignoring model names the LLM invents via the spawn_agent `model` arg.
func resolveAgentModel(requested, main string, configured map[string]bool) string {
	if requested != "" && (requested == main || configured[requested]) {
		return requested
	}
	return main
}

// agentModelSet collects the non-empty per-agent-type model overrides so the
// agent LLM factory can tell a configured model apart from an invented one.
func agentModelSet(defs []cogito.AgentDefinition) map[string]bool {
	m := make(map[string]bool, len(defs))
	for _, d := range defs {
		if d.Model != "" {
			m[d.Model] = true
		}
	}
	return m
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
		cogitoOptions: cfg.AgentOptions,
		allowedTools:  make(map[string]bool),
		agentManager:  agentManager,
		agentLogs:     newAgentLogStore(),
		llmModel:      cfg.Model,
		apiKey:        cfg.APIKey,
		baseURL:       cfg.BaseURL,
		mcpClient:     client,
		cfgClients:    map[string]*mcp.ClientSession{},
		cfgServers:    map[string]types.MCPServer{},
		configurator:  manage.New(plugin.BaseDir(), config.WritablePath()),
	}
	for _, name := range cfg.AllowedTools {
		s.allowedTools[name] = true
	}
	s.autoApprove = cfg.ApprovalMode == "auto"
	// Wire reloadable state (skills server, config MCP clients, agents, hooks,
	// system prompt) through the same path used for live reloads.
	if err := s.Reload(cfg); err != nil {
		xlog.Warn("self-config: initial reload", "error", err)
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
			suffix := "\n\n# Skill: " + sk.Name + "\n" + sk.Instructions
			s.loadedSkills += suffix
			s.systemPrompt += suffix
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

	if s.autoApprove || s.allowAllTurn {
		return cogito.ToolCallDecision{Approved: true}
	}
	if s.allowedTools[req.Name] {
		return cogito.ToolCallDecision{Approved: true}
	}
	if s.callbacks.OnToolCall == nil {
		return cogito.ToolCallDecision{Approved: true}
	}
	resp := s.callbacks.OnToolCall(req)
	if resp.Approved && resp.AllowAllTurn {
		s.allowAllTurn = true
	}
	if resp.Approved && resp.AlwaysAllow {
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

// KillAgent cancels a running sub-agent by id (its context is cancelled, which
// stops the agent and any LLM call it has in flight). Returns false when the id
// is unknown. Safe to call on an already-finished agent.
func (s *Session) KillAgent(id string) bool {
	if s.agentManager == nil {
		return false
	}
	a, ok := s.agentManager.Get(id)
	if !ok {
		return false
	}
	if a.Cancel != nil {
		a.Cancel()
	}
	return true
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
	s.applyPendingReload()
	s.allowAllTurn = false
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
		cogito.WithMCPs(s.allClients()...),
		cogito.WithToolCallBack(func(tool *cogito.ToolChoice, state *cogito.SessionState) cogito.ToolCallDecision {
			// Capture sub-agent activity so the agent_logs tool can surface what
			// a backgrounded sub-agent is doing.
			if state.AgentID != "" {
				s.agentLogs.recordCall(state.AgentID, tool)
			}
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
			s.agentLogs.recordResult(status) // no-op for root-agent tool calls
			if s.hooks != nil {
				s.hooks.Fire(s.ctx, hooks.EventPostToolUse, status.Name, map[string]any{
					"event":  "PostToolUse",
					"tool":   status.Name,
					"result": status.Result,
				})
			}
			if s.callbacks.OnToolResult != nil {
				argsJSON := ""
				if b, err := json.Marshal(status.ToolArguments.Arguments); err == nil {
					argsJSON = string(b)
				}
				s.callbacks.OnToolResult(ToolResult{
					Name:      status.Name,
					Result:    status.Result,
					Arguments: argsJSON,
					AgentID:   s.agentLogs.agentFor(status.ToolArguments.ID),
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
			// The spawn_agent tool lets the LLM request a `model`, which it may
			// fill with a name the endpoint doesn't serve (e.g. "sonar") and
			// 404 the sub-agent. Honor a requested model only when wiz actually
			// serves it — the main model, or one configured for an agent type —
			// otherwise fall back to the main model. This keeps per-agent model
			// overrides from config working while ignoring invented names.
			chosen := resolveAgentModel(model, s.llmModel, s.agentModels)
			if model != "" && chosen != model {
				xlog.Warn("sub-agent requested an unserved model; using the main model",
					"requested", model, "model", chosen)
			}
			return clients.NewOpenAILLMWithOptions(chosen, s.apiKey, s.baseURL, clients.OpenAIOptions{Temperature: temperature})
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
		cogito.WithTools(agentLogsToolDefinition(s.AgentLog)),
		cogito.WithTools(scheduleWakeupToolDefinition(func(req WakeupRequest) string {
			if s.callbacks.OnScheduleWakeup != nil {
				return s.callbacks.OnScheduleWakeup(req)
			}
			return "Scheduling is not available in this session."
		})),
	)

	// Wire the native self-configuration tools so the assistant can manage its
	// own plugins, skills, and MCP servers. requestReload re-wires the live
	// session on the next turn after any mutating op.
	for _, d := range selfConfigToolDefs(s.configurator, s.requestReload) {
		cogitoOpts = append(cogitoOpts, cogito.WithTools(d.def))
	}

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

// allClients returns every connected MCP client (built-ins + skills + config
// servers). Called from the turn goroutine. Reload mutates these only at turn
// start (and is deferred while background sub-agents run, see
// applyPendingReload), so it does not race with detached agents and no lock is
// needed.
func (s *Session) allClients() []*mcp.ClientSession {
	out := append([]*mcp.ClientSession{}, s.clients...)
	if s.skillsClient != nil {
		out = append(out, s.skillsClient)
	}
	for _, c := range s.cfgClients {
		out = append(out, c)
	}
	return out
}

// ReconcileMCPServers connects newly-desired config MCP servers and closes ones
// no longer desired (or whose command/args changed). Connect failures are
// logged and skipped so one bad server never breaks the session. Called from
// Reload at turn start (deferred while background sub-agents run), so closing a
// client session here cannot race with a detached agent still using it.
func (s *Session) ReconcileMCPServers(desired map[string]types.MCPServer) error {
	for name, sess := range s.cfgClients {
		if d, ok := desired[name]; !ok || !reflect.DeepEqual(d, s.cfgServers[name]) {
			_ = sess.Close()
			delete(s.cfgClients, name)
			delete(s.cfgServers, name)
		}
	}
	for name, srv := range desired {
		if _, ok := s.cfgClients[name]; ok {
			continue
		}
		var env []string
		for k, v := range srv.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		transport := CommandTransport(srv.Command, srv.Args, env...)
		sess, err := s.mcpClient.Connect(s.ctx, transport, nil)
		if err != nil {
			xlog.Warn("self-config: MCP server failed to connect", "name", name, "error", err)
			continue
		}
		s.cfgClients[name] = sess
		s.cfgServers[name] = srv
	}
	return nil
}

// SetSkills rebuilds the in-memory skills MCP server so load_skill advertises
// the given skills, swapping its client. An empty list tears the server down.
// Called from Reload at turn start (deferred while background sub-agents run),
// so closing the old skills client cannot race with a detached agent.
func (s *Session) SetSkills(skills []types.Skill) error {
	if s.skillsClient != nil {
		_ = s.skillsClient.Close()
		s.skillsClient = nil
	}
	s.skills = skills
	if len(skills) == 0 {
		return nil
	}
	serverT, clientT := mcp.NewInMemoryTransports()
	go func() {
		if err := wizmcp.StartSkillsMCPServer(s.ctx, serverT, skills); err != nil {
			xlog.Warn("self-config: skills MCP server error", "error", err)
		}
	}()
	sess, err := s.mcpClient.Connect(s.ctx, clientT, nil)
	if err != nil {
		return err
	}
	s.skillsClient = sess
	return nil
}

// Reload re-wires every reloadable part of the session from cfg. It closes MCP
// client sessions and mutates session state read by the turn goroutine and by
// detached sub-agents, so it must run at turn start in the turn goroutine and is
// deferred while background sub-agents run (see applyPendingReload). It must not
// run concurrently with a running turn or a live detached agent.
func (s *Session) Reload(cfg types.Config) error {
	_ = s.ReconcileMCPServers(cfg.MCPServers)
	_ = s.SetSkills(cfg.Skills)
	s.agentDefs = toCogitoDefinitions(cfg.Agents)
	s.agentModels = agentModelSet(s.agentDefs)
	s.hooks = hooks.New(cfg.Hooks)
	if cfg.Prompt != "" {
		s.systemPrompt = cfg.GetPrompt() + s.loadedSkills
	}
	return nil
}

// requestReload marks the session dirty; the next SendMessage applies it.
func (s *Session) requestReload() {
	s.reloadMu.Lock()
	s.pendingReload = true
	s.reloadMu.Unlock()
}

// applyPendingReload, if a reload was requested, recomputes the effective config
// and re-wires the session. Runs at the start of a turn, in the turn goroutine.
// It DEFERS the reload while background sub-agents are still running: Reload
// closes MCP client sessions and mutates session state (s.hooks, s.skillsClient,
// s.cfgClients, s.agentDefs) that detached agents — which outlive their spawning
// turn — continue to read, so reconfiguring under them would race or use a closed
// session. The dirty flag stays set, so the reload is retried on a later turn
// once no background agents are live. The flag is cleared only after a
// SUCCESSFUL reload, so a failed reload is retried on the next turn.
func (s *Session) applyPendingReload() {
	s.reloadMu.Lock()
	pending := s.pendingReload
	s.reloadMu.Unlock()
	if !pending || s.configurator == nil {
		return
	}
	if s.agentManager != nil && s.agentManager.HasRunning() {
		return // defer; pendingReload stays set, retried next turn
	}
	eff, err := s.configurator.EffectiveConfig()
	if err != nil {
		xlog.Warn("self-config: effective config", "error", err)
		return // leave flag set, retried next turn
	}
	if err := s.Reload(eff); err != nil {
		xlog.Warn("self-config: reload", "error", err)
		return // leave flag set, retried next turn
	}
	s.reloadMu.Lock()
	s.pendingReload = false
	s.reloadMu.Unlock()
}

// Close closes the session and cleans up resources
func (s *Session) Close() error {
	var firstErr error
	for _, client := range s.clients {
		if err := client.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.skillsClient != nil {
		_ = s.skillsClient.Close()
	}
	for _, c := range s.cfgClients {
		_ = c.Close()
	}
	return firstErr
}
