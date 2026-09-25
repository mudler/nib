package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/nib/auth"
	_ "github.com/mudler/nib/classify/systemone" // the SystemOne classifier API
	"github.com/mudler/nib/endpoint"
	"github.com/mudler/nib/hooks"
	"github.com/mudler/nib/internal"
	"github.com/mudler/nib/llmprovider"
	"github.com/mudler/nib/llmprovider/copilot"
	"github.com/mudler/nib/lsp"
	"github.com/mudler/nib/manage"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/plugin"
	"github.com/mudler/nib/provenance"
	"github.com/mudler/nib/provider"
	"github.com/mudler/nib/specialist"
	"github.com/mudler/nib/trace"
	"github.com/mudler/nib/types"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/cogito"
	"github.com/mudler/xlog"
	openai "github.com/sashabaranov/go-openai"
)

// Session represents a chat session with the AI assistant
type Session struct {
	ctx          context.Context
	turnMu       sync.Mutex
	turnCancel   context.CancelFunc
	llm          cogito.LLM // guarded by modelMu
	clients      []*mcp.ClientSession
	mcpClient    *mcp.Client
	cfgClients   map[string]*mcp.ClientSession // config/plugin MCP servers, by name
	cfgServers   map[string]types.MCPServer    // desired set, for diffing
	skillsClient *mcp.ClientSession            // the load_skill server
	// historyMu guards the parallel conversation state — fragment (the real
	// model context handed to cogito) and messages (the {role,content} log
	// surfaced to the UI). SendMessage mutates both as a turn progresses; the
	// lock lets ExportHistory take a consistent copy from another goroutine
	// while a turn is running.
	historyMu            sync.Mutex
	fragment             cogito.Fragment
	messages             []openai.ChatCompletionMessage
	callbacks            Callbacks
	systemPrompt         string
	loadedSkills         string // eager-loaded /skill instructions, re-applied across reloads
	skills               []types.Skill
	cogitoOptions        types.AgentOptions
	compaction           types.CompactionConfig
	allowedTools         map[string]bool    // Tools that don't need approval this session
	toolAllow            map[string]bool    // if non-empty, the only built-in tools exposed to the model
	allowedBashPrefixes  map[string]bool    // bash first-word grants ("git" → simple `git …` auto-approved)
	autoApprove          atomic.Bool        // approval_mode: auto, or the /yolo toggle — approve every tool call
	allowAllTurn         bool               // user chose "allow all this turn"; reset each top-level turn
	approvalMode         types.ApprovalMode // raw approval_mode, "" meaning prompt; guarded by approvalMu
	approvalMu           sync.RWMutex       // guards approvalMode: /settings changes it while a turn reads it
	readOnlyCommands     readOnlyCommands   // bash commands auto-approved in prompt mode
	hooks                *hooks.Dispatcher
	provenanceMu         sync.Mutex
	externalSources      map[string]provenance.Envelope
	externalToolNames    map[string]bool // tools supplied by configured/plugin MCP servers
	provenanceClassifier provenance.Classifier
	// cls is the small classification model (classifier:) and what is
	// built on it; nil when none is configured. See classifierState.
	cls atomic.Pointer[classifierState]

	agentMu    sync.Mutex
	agentStart map[string]time.Time // sub-agent ID -> spawn time, for elapsed

	// inject is the per-session message-injection channel handed to cogito. While
	// a run parks (background work pending, or simply waiting for the user), the
	// loop blocks on this channel; sending a message wakes it and continues the
	// SAME run. Shell-job completions, scheduled wake-ups, and mid-run user
	// follow-ups all flow through here. Buffered so non-blocking sends rarely drop.
	inject chan openai.ChatCompletionMessage
	// runLive is set while ExecuteTools is in flight (between SendMessage start
	// and return), so Inject can tell whether there is a live run to inject into.
	runMu   sync.Mutex
	runLive bool
	// userInjected tracks texts delivered via InjectUser during the current
	// run; undelivered collects the ones still sitting in the injection
	// channel when the run returned — the model never saw them, so the
	// end-of-run drain hands them back via TakeUndelivered instead of
	// discarding them. Both guarded by runMu.
	userInjected []string
	undelivered  []string

	// pendingNotices holds background-job notices that arrived while no run was
	// live, for the next turn. Guarded by runMu.
	pendingNotices []string

	// goal is the active session goal (the /goal stop-gate). While non-empty,
	// SendMessage re-runs the turn until the model calls goal_done. goalDone is
	// set by that tool within a run. goalPaused keeps the goal text but stops
	// the pursuit: an interrupt pauses the goal instead of clearing it, so
	// Ctrl+C does not silently throw away what the user asked for. All guarded
	// by runMu.
	goal       string
	goalDone   bool
	goalPaused bool

	// todoList is the ephemeral in-memory todo list (todo_write tool). It is
	// not persisted — it lives for the session and is cleared when the session
	// ends. Mirrors maki's design: the model sends the full list on every call.
	todoList *TodoList

	// shellJobs lets the pending-work predicate keep the run parked while a
	// backgrounded shell command is still running (cogito only knows about
	// sub-agents). May be nil (e.g. headless CLI without a job registry).
	shellJobs *wizmcp.ShellJobs

	// schemaTools records the tool definitions toolOptions registers, for
	// SchemaBudget. Guarded by schemaToolsMu. See schema_budget.go.
	schemaTools   []cogito.ToolDefinitionInterface
	schemaToolsMu sync.Mutex

	// schemaCosts caches mcpSchemaCosts until the set of MCP servers changes;
	// schemaNoticeLevel is the highest SchemaBudget level (1 warn, 2 error)
	// already reported to the user. Both guarded by schemaCostsMu.
	schemaCosts       []ServerSchemaCost
	schemaCostsValid  bool
	schemaNoticeLevel int
	schemaCostsMu     sync.Mutex

	agentManager       *cogito.AgentManager
	agentDefs          []cogito.AgentDefinition
	agentModels        map[string]bool // models configured per agent type (for the LLM-model guard)
	endpointModels     []string        // models the endpoint advertises (lazy-fetched on first spawn_agent)
	endpointModelsOnce sync.Once       // guards the one-time lazy fetch in allowedAgentModels
	agentLogs          *agentLogStore  // per-sub-agent activity log (for the agent_logs tool)

	// live carries the prompt-token count of the turn in flight, so the
	// context gauge advances with each call of a multi-step turn instead of
	// jumping once at the end. See liveUsage.
	live liveUsage

	// modelMu guards the (llm, llmModel) pair, which SetModel replaces together
	// when the user switches model mid-session. Not turnMu: that one is held
	// only across beginTurn/endTurn/Interrupt (it guards turnCancel, not the
	// turn), so taking it here would order nothing against a running request.
	// Readers snapshot the pair through currentLLM/Model, which is what keeps a
	// turn on one client from start to finish while the switch applies from the
	// next one. The rest of the endpoint state below (apiKey, baseURL,
	// metadata, reasoningEffort) is fixed at construction and read lock-free.
	modelMu      sync.RWMutex
	llmModel     string                    // guarded by modelMu
	mainProvider types.ModelProviderConfig // guarded by modelMu
	endpointID   string                    // guarded by modelMu; endpoint.DefaultID until switched
	// configProvider is the endpoint config.yaml describes, kept so the
	// provider picker can switch back to it after using a named endpoint or a
	// /login provider.
	configProvider types.ModelProviderConfig
	// endpoints is the resolvable set of endpoints for this config: the
	// config.yaml default, its named endpoints, and the provider registry.
	endpoints *endpoint.Set
	// configErrs are the config.yaml endpoints rejected while building
	// endpoints, for the boot log.
	configErrs []error
	// startupNote is set by restoreStartupEndpoint when a saved pick could
	// not be honored at startup, for the boot log.
	startupNote string
	// savedPath is where the picked endpoint is kept (ProviderStateFile),
	// next to credentials.json. NewSession always sets it (plugin.BaseDirIn
	// never resolves to ""); only a Session built directly within this
	// package's own tests can leave it unset.
	savedPath string
	credStore *auth.Store // credential store for /login-managed providers

	// learnedWindow is the context window a backend stated in an overflow
	// error, and learnedWindowModel is the model it was learned for. They are
	// guarded by modelMu because they are only ever meaningful as a pair with
	// llmModel, and reading them under a different lock would let a model
	// switch land between the two reads.
	learnedWindow      int
	learnedWindowModel string
	apiKey             string
	baseURL            string
	transcribeModel    string
	visionModel        string
	videoModel         string
	workingDir         string
	metadata           map[string]string // global per-request metadata; merged with per-agent overrides
	reasoningEffort    string            // OpenAI reasoning_effort sent on every request (e.g. "none")

	// lspManager owns the LSP server processes, one per language. It is
	// nil when no servers are configured, which suppresses the lsp tool.
	// Set once in NewSession; read-only afterwards. Closed in Close().
	lspManager *lsp.Manager

	// changes holds pre-call file snapshots of in-flight write/edit calls,
	// so their results can be shown as diffs.
	changes changeTracker

	configurator  *manage.Configurator
	reloadMu      sync.Mutex
	pendingReload bool

	// computerEnabled records whether desktop control (computer_use) was armed
	// for this session. It gates cogito's Phase-A tool-image forwarding so
	// screenshots are only fed back to the model when a computer transport is
	// actually registered. Fixed at construction (Computer config is runtime-only).
	computerEnabled bool

	// prefixWarm records whether this session has actually issued a request that
	// prefilled its prompt prefix (system prompt + tool schemas) on the server.
	// On CPU hardware that prefill costs tens of seconds on the first request and
	// nothing afterwards, so a UI reads this to label the cold turn rather than
	// leaving the user staring at a silent minute.
	//
	// An atomic rather than a mutex-guarded bool on purpose: PrefixWarm is read
	// from the UI goroutine while a turn holds runMu/historyMu, and Warm
	// deliberately avoids holding runMu across its network call.
	prefixWarm atomic.Bool

	// usage is this session's running token total. See chat/usage.go for why it
	// carries its own lock rather than sharing historyMu.
	usage sessionUsage

	// compactionAutoDetected records whether MaxContextTokens was auto-detected
	// (from the endpoint probe or static table) rather than explicitly set by
	// the user. When true, SetModel re-runs detection for the new model; when
	// false, the user's explicit value is preserved across model switches.
	compactionAutoDetected bool

	// limitsFor is the model whose context window and output cap have already
	// been asked of the endpoint; guarded by modelMu. Empty means the probes
	// are still owed, which is the state a model switch returns it to. See
	// modellimits.go.
	limitsFor string

	// outputCap is the output-token cap the current model's client sends,
	// as resolved for it (config, catalog, then discovery); guarded by
	// modelMu. 0 means unknown, or a client that carries no cap. The turn's
	// LLM wrapper clamps each request's reservation against it (see
	// clampOutputTokens).
	outputCap int
	// turnOutputCap, when above 0, lowers outputCap for the current turn
	// only; guarded by modelMu. A budget overflow sets it from the figures
	// the backend stated (see budgetRetryOutput), and each turn clears it.
	turnOutputCap int

	// prunedMu guards the tool-output pruning state below. The manipulator reads
	// it from inside cogito's loop, and nothing here should assume which
	// goroutine that is; Reload writes the policy from the turn goroutine.
	prunedMu sync.Mutex
	// pruning is the tool-output pruning policy for this session.
	pruning types.ToolOutputPruningConfig
	// prunedIDs maps each tool_call_id already replaced by a stub to the clause
	// that stub carries. It is what makes pruning monotonic across calls, and it
	// is why a prune notice fires on a transition rather than on every LLM call.
	//
	// The clause is stored rather than re-derived because the reason a result
	// was dropped can change under the policy's feet — a result swept for budget
	// becomes a stale read as soon as the model edits the file it had read — and
	// a stub whose wording changed between calls would move the prompt prefix
	// just as un-stubbing it would.
	prunedIDs map[string]string
	// compressed maps each tool_call_id progressivePrune compressed to its
	// level and the text it was rendered as. Like prunedIDs it only grows in
	// level, so an already-sent result keeps its bytes. compressBand is the
	// pressure band of the previous call; levels are assigned only when the
	// band rises above it.
	compressed   map[string]compressedResult
	compressBand int

	// outputLimitsMu guards the tool-output limits policy (budget, per-line
	// truncation, artifact spill). SetToolOutputLimits writes it from the UI
	// goroutine; the MCP tool handlers read it from inside cogito's loop.
	outputLimitsMu sync.RWMutex
	outputLimits   types.ToolOutputLimitsConfig
	// artifacts is the session-scoped store for spilled tool output. When a
	// tool result exceeds the spill threshold, the full output is saved here
	// and the model gets a head+tail slice plus an artifact://N reference.
	artifacts *wizmcp.ArtifactStore

	// overflowRetried counts context-overflow recoveries in the CURRENT turn.
	// It exists so a retry can never become a loop, and so tests can assert on
	// attempts rather than on a raw completion-call count that cogito's own
	// internal retries make unstable.
	overflowMu      sync.Mutex
	overflowRetried int

	// turnRetryTotal counts the times the CURRENT turn was run again after
	// a rate-limited or transient backend error. See retry.go.
	turnRetryMu    sync.Mutex
	turnRetryTotal int

	// agentBackoff tells the stall detector when sub-agents wait on the
	// backend. See agentretry.go.
	agentBackoff agentBackoff

	tracer *trace.Recorder // non-nil when session tracing is enabled

	// memoryStore persists notes written via the memory tool. Rooted at the
	// per-user BaseDir (like the session store) so notes survive across
	// sessions and compaction.
	memoryStore *MemoryStore

	// traceDir is where usage.json is written on Close. Empty = tracing off, so
	// an untraced session leaves nothing behind. Kept separate from tracer
	// because usage.json is written beside the transcript rather than through
	// the recorder: it shares no state with it, and holding the directory here
	// keeps the report reachable independently of the recorder's lifetime.
	traceDir string

	// contextNudgePending is set when end-of-turn compaction succeeds, and
	// cleared once the model re-reads a detected context file (AGENTS.md etc.)
	// or the nudge fires — whichever comes first. When true, the next tool
	// call in the following turn injects a one-time user-role nudge to
	// re-read context files before acting. Guarded by historyMu.
	contextNudgePending bool
}

// PrefixWarm reports whether this session has already issued a request that
// SHOULD have prefilled its prompt prefix on the server — not that the server's
// cache still holds it. A LocalAI restart or a KV eviction leaves this
// stale-true. That direction is deliberate: a stale true costs a missing
// "preparing" label, while a false negative would only cost a redundant one, so
// callers should treat it as a hint for labelling and never as a guarantee.
//
// It is set only where the model was genuinely reached — a completed
// SendMessage turn or a successful Warm — and never by the paths that bail
// before that (an attachments failure, or an all-blocked attachment set with no
// text). It is safe to call from another goroutine while a turn is running.
//
// SetModel resets it to false: unlike a restart or an eviction, a switch is a
// cold prefix the session knows about. Nothing else resets it, and a rebuilt
// session is a new *Session that starts false.
func (s *Session) PrefixWarm() bool {
	return s.prefixWarm.Load()
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
// wiz actually serves it (the main model, one configured for an agent type, or
// one advertised by the endpoint's model list), otherwise the main model. This
// honors per-agent model overrides from config and endpoint-served models while
// ignoring model names the LLM invents via the spawn_agent `model` arg.
func resolveAgentModel(requested, main string, allowed map[string]bool) string {
	if requested != "" && (requested == main || allowed[requested]) {
		return requested
	}
	return main
}

// allowedAgentModels returns the set of models a sub-agent may use: those
// configured per agent type (config `agents:`) plus those the endpoint
// advertises via /v1/models. The main model is always implicitly allowed
// (checked separately in resolveAgentModel).
//
// The endpoint model list is fetched lazily on the first call (i.e. the first
// spawn_agent), not during NewSession, so session init never makes an HTTP
// call. On a successful fetch the session is marked for reload so the next
// turn's system prompt carries the guidance listing. Tests that pre-set
// endpointModels bypass the fetch.
func (s *Session) allowedAgentModels() map[string]bool {
	s.endpointModelsOnce.Do(func() {
		if s.endpointModels == nil && s.baseURL != "" {
			s.fetchEndpointModels(context.Background())
			if len(s.endpointModels) > 0 {
				s.requestReload()
			}
		}
	})
	allowed := make(map[string]bool, len(s.agentModels)+len(s.endpointModels))
	for m := range s.agentModels {
		allowed[m] = true
	}
	for _, m := range s.endpointModels {
		allowed[m] = true
	}
	return allowed
}

// newAgentLLM builds the LLM client for a spawned sub-agent. mainModel is the
// session model this turn runs against; requested is what the spawn_agent tool
// asked for, which the LLM may fill with a name the endpoint doesn't serve
// (e.g. "sonar") and 404 the sub-agent. Honor a requested model only when wiz
// actually serves it (the main model, a configured agent-type model, or one
// advertised by the endpoint), otherwise fall back to the main model. This keeps
// per-agent model overrides from config working while ignoring invented names.
func (s *Session) newAgentLLM(mainModel, requested string, temperature float32, metadata map[string]string) cogito.LLM {
	chosen := resolveAgentModel(requested, mainModel, s.allowedAgentModels())
	if requested != "" && chosen != requested {
		xlog.Warn("sub-agent requested an unserved model; using the main model",
			"requested", requested, "model", chosen)
	}
	// metadata is this agent type's override; overlay it on the global
	// session metadata (per-key: agent wins, global-only keys inherited).
	provider := s.resolvedSessionProvider()
	provider.Model = chosen
	provider.Metadata = mergeMetadata(s.metadata, metadata)
	provider.ReasoningEffort = s.reasoningEffort
	agentLLM, err := llmprovider.NewWithTemperatureAndStore(provider, temperature, s.credStore)
	if err != nil {
		xlog.Warn("could not create sub-agent LLM; using the current main LLM", "error", err)
		agentLLM, _ = s.currentLLM()
	}
	return retryForAgent(agentLLM, &s.agentBackoff)
}

func (s *Session) resolvedSessionProvider() types.ModelProviderConfig {
	s.modelMu.RLock()
	defer s.modelMu.RUnlock()
	if s.mainProvider.Configured() {
		return s.mainProvider
	}
	return types.ModelProviderConfig{
		Provider:        "openai",
		Model:           s.llmModel,
		APIKey:          s.apiKey,
		BaseURL:         s.baseURL,
		Metadata:        s.metadata,
		ReasoningEffort: s.reasoningEffort,
	}
}

// mergeMetadata overlays per-agent metadata on top of the global metadata,
// returning a new map (or nil when both are empty). Per-agent keys win; keys
// present only in the global map are inherited. Inputs are never mutated.
func mergeMetadata(global, override map[string]string) map[string]string {
	if len(global) == 0 && len(override) == 0 {
		return nil
	}
	merged := make(map[string]string, len(global)+len(override))
	for k, v := range global {
		merged[k] = v
	}
	for k, v := range override {
		merged[k] = v
	}
	return merged
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
			Metadata:     t.Metadata,
			Iterations:   t.Iterations,
			MaxAttempts:  t.MaxAttempts,
			MaxRetries:   t.MaxRetries,
		})
	}
	return defs
}

// NewSession creates a new chat session.
//
// If cfg.TraceDir is set and the recorder cannot be opened, NewSession fails
// rather than returning a session that quietly records nothing — a caller that
// asked for a trace should not have to discover later that it never got one.
// app.Run preflights the directory, so in practice only embedders calling this
// directly reach the failure.
func NewSession(ctx context.Context, cfg types.Config, callbacks Callbacks, transports ...mcp.Transport) (*Session, error) {
	// LocalAIClient, not OpenAIClient: LocalAI (and vLLM) put reasoning/thinking
	// text in a "reasoning" response field, which go-openai's SDK — and so
	// OpenAIClient — doesn't know about (it only binds the older, now-deprecated
	// "reasoning_content" key). LocalAIClient parses both.
	credStore := auth.NewStore(filepath.Join(plugin.BaseDirIn(cfg.BaseDir), "credentials.json"))
	mainProvider := cfg.ResolvedMainModel()
	llm, err := llmprovider.NewWithStore(mainProvider, credStore)
	if err != nil {
		return nil, fmt.Errorf("create main LLM: %w", err)
	}
	// Read before tracing wraps the client and hides its SetMaxTokens.
	outCap := factoryOutputCap(llm, mainProvider, credStore)
	endpoints, configErrs := endpoint.New(cfg, credStore)
	classifier, err := provenance.ClassifierForConfig(cfg)
	if err != nil {
		return nil, err
	}
	// A broken classifier block costs the features built on it, not the
	// session: it is reported with the rejected endpoints.
	smallClassifier, err := buildClassifier(cfg)
	if err != nil {
		configErrs = append(configErrs, err)
	}

	// Session tracing: wrap the LLM so every call is appended to the transcript.
	// Tracing is only ever on because someone asked for it, so a recorder that
	// cannot open is a failure rather than a downgrade. This used to be an
	// xlog.Warn, which the default "error" log level discarded — the session
	// then ran untraced and said nothing, which is exactly what was reported.
	// app.Run preflights this so the usual failure never reaches here; what is
	// left is the dir vanishing between check and open, and embedders calling
	// NewSession directly.
	var tracer *trace.Recorder
	if cfg.TraceDir != "" {
		rec, err := trace.NewRecorder(cfg.TraceDir)
		if err != nil {
			return nil, fmt.Errorf("cannot write trace to %s: %w", cfg.TraceDir, err)
		}
		tracer = rec
		llm = trace.NewRecordingLLM(llm, rec, mainProvider.Model, "")
	}

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
		ctx:                  ctx,
		llm:                  llm,
		clients:              clients,
		fragment:             cogito.NewEmptyFragment(),
		messages:             []openai.ChatCompletionMessage{},
		callbacks:            callbacks,
		inject:               make(chan openai.ChatCompletionMessage, 16),
		cogitoOptions:        cfg.AgentOptions,
		compaction:           cfg.Compaction,
		pruning:              cfg.ToolOutputPruning,
		outputLimits:         cfg.ToolOutputLimits,
		artifacts:            wizmcp.NewArtifactStore(),
		allowedTools:         make(map[string]bool),
		toolAllow:            make(map[string]bool),
		allowedBashPrefixes:  make(map[string]bool),
		agentStart:           make(map[string]time.Time),
		agentManager:         agentManager,
		agentLogs:            newAgentLogStore(),
		llmModel:             mainProvider.Model,
		mainProvider:         mainProvider,
		outputCap:            outCap,
		configProvider:       mainProvider,
		endpoints:            endpoints,
		configErrs:           configErrs,
		savedPath:            filepath.Join(plugin.BaseDirIn(cfg.BaseDir), ProviderStateFile),
		endpointID:           endpoint.DefaultID,
		credStore:            credStore,
		apiKey:               mainProvider.APIKey,
		baseURL:              mainProvider.BaseURL,
		transcribeModel:      cfg.TranscribeModel,
		visionModel:          cfg.VisionModel,
		videoModel:           cfg.VideoModel,
		workingDir:           cfg.WorkingDir,
		metadata:             mainProvider.Metadata,
		reasoningEffort:      mainProvider.ReasoningEffort,
		mcpClient:            client,
		computerEnabled:      cfg.Computer.Enabled,
		cfgClients:           map[string]*mcp.ClientSession{},
		cfgServers:           map[string]types.MCPServer{},
		configurator:         manage.NewIn(cfg.BaseDir),
		memoryStore:          NewMemoryStore(filepath.Join(plugin.BaseDirIn(cfg.BaseDir), "memory")),
		todoList:             NewTodoList(),
		tracer:               tracer,
		traceDir:             cfg.TraceDir,
		provenanceClassifier: classifier,
	}

	// Build the LSP server set: explicit config first, then auto-detect
	// any servers on PATH that aren't already explicitly configured.
	lspConfigs := make(map[string]lsp.ServerConfig)
	for lang, s := range cfg.LSP {
		lspConfigs[lang] = lsp.ServerConfig{
			Command: s.Command,
			Args:    s.Args,
			Env:     s.Env,
		}
	}
	autoDetect := true
	if cfg.LSPAutoDetect != nil {
		autoDetect = *cfg.LSPAutoDetect
	}
	if autoDetect {
		explicit := make(map[string]bool, len(lspConfigs))
		for lang := range lspConfigs {
			explicit[lang] = true
		}
		detected := lsp.AutoDetect(explicit)
		for lang, dc := range detected {
			lspConfigs[lang] = dc
		}
		if len(detected) > 0 {
			for _, line := range lsp.DetectedServers(explicit) {
				xlog.Info("Language server detected: " + line)
			}
		}
	}
	if len(lspConfigs) > 0 {
		s.lspManager = lsp.NewManager(lspConfigs, cfg.WorkingDir)
	}
	// Resume/rehydration: seed a prior conversation so the very next SendMessage
	// continues with full memory of it, behaving identically to a session that
	// organically reached this state. We seed BOTH parallel fields:
	//   - s.messages: the {role,content} log the UI reads back.
	//   - s.fragment: the real model context handed to cogito on the next turn.
	// The seeded history MUST NOT contain the system prompt (ExportHistory never
	// emits one, and Config documents the contract). SendMessage prepends the
	// current system prompt at the START of every turn via
	// s.fragment.AddMessage("system", …) — so we deliberately seed the fragment
	// WITHOUT a system message and let that per-turn add supply exactly one. That
	// is what makes a resumed turn structurally identical to a fresh multi-turn
	// session (whose fragment likewise carries no leading system message before
	// the turn's own add), and it avoids a duplicated or missing system prompt on
	// the first resumed turn. Token counters reset to zero and are recomputed from
	// the first resumed request's usage, which reflects the full seeded context.
	if len(cfg.InitialHistory) > 0 {
		seed := slices.Clone(cfg.InitialHistory)
		s.messages = seed
		s.fragment = cogito.NewFragment(seed...)
	}
	// A resumed session carries on with its goal, paused or not. Paused
	// means nothing without a goal, as in PauseGoal.
	s.goal = cfg.InitialGoal
	s.goalPaused = cfg.InitialGoalPaused && cfg.InitialGoal != ""
	for _, name := range cfg.AllowedTools {
		s.allowedTools[name] = true
	}
	for _, name := range cfg.BuiltinTools {
		s.toolAllow[name] = true
	}
	s.cls.Store(smallClassifier)
	if err := validateAutoApprove(cfg.AutoApprove); err != nil {
		s.configErrs = append(s.configErrs, err)
	}
	s.autoApprove.Store(cfg.ApprovalMode == types.ApprovalAuto)
	s.approvalMode = cfg.ApprovalMode
	if cfg.ApprovalMode == types.ApprovalClassify && smallClassifier == nil {
		s.configErrs = append(s.configErrs, fmt.Errorf("approval_mode: classify needs a classifier block; using prompt"))
		s.approvalMode = types.ApprovalPrompt
	}
	s.readOnlyCommands = newReadOnlyCommands(cfg.ReadOnlyCommands)
	// Wire reloadable state (skills server, config MCP clients, agents, hooks,
	// system prompt) through the same path used for live reloads.
	if err := s.Reload(cfg); err != nil {
		xlog.Warn("self-config: initial reload", "error", err)
	}
	s.hooks.Fire(ctx, hooks.EventSessionStart, "", map[string]any{"event": "SessionStart"})

	// An endpoint picked in an earlier session is the default. A resumed
	// session then goes back to the endpoint and model it was using.
	s.restoreStartupEndpoint()
	s.restoreResumedModel(cfg.InitialEndpoint, cfg.InitialModel)

	// A zero MaxContextTokens means "unset" (config.go no longer defaults it).
	// The default applies immediately so compaction and the gauge always have
	// a figure; the endpoint is asked for the model's real window at the start
	// of the first turn, in ensureModelLimits, rather than here. Building a
	// session makes no request, so nib starts on a machine with no network.
	if s.compaction.MaxContextTokens == 0 {
		s.compactionAutoDetected = true
		s.compaction.MaxContextTokens = defaultContextTokens
	}
	setConfigOverflowPatterns(cfg.Compaction.OverflowPatterns)

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

// SetAutoApprove turns the session-wide approve-everything switch on or off at
// runtime (the /yolo toggle). It is deliberately atomic rather than guarded by
// historyMu, which is held across whole tool calls.
//
// It does not touch allowedTools or allowedBashPrefixes: those are narrower
// grants the user minted explicitly, and revoking them as a side effect of
// flipping this switch would be a surprise. While active, it bypasses the
// external-influence approval prompt but still permits PreToolUse hooks to
// enforce their policies.
func (s *Session) SetAutoApprove(on bool) { s.autoApprove.Store(on) }

// AutoApprove reports whether every tool call is currently auto-approved.
func (s *Session) AutoApprove() bool { return s.autoApprove.Load() }

// decideToolCall resolves a tool-call request: PreToolUse hooks first (a hook
// may block/approve/adjust), then the session allow-list, then the user gate.
// emitSubAgentToolLine surfaces a sub-agent's tool call as a compact inline
// thread line via OnToolResult, from the tool-CALL callback where the AgentID is
// reliable. cogito propagates the tool-call callback into spawned sub-agents
// (with state.AgentID set) but NOT the tool-result callback, so OnToolResult
// never fires for a sub-agent's tools on its own — keying the thread off the
// result callback would show nothing. It is a no-op for the root agent
// (agentID == "", whose tools stream with their output via the result
// callback), for denied calls, and when no callback is registered.
func (s *Session) emitSubAgentToolLine(approved bool, agentID, name, args string) {
	if !approved || agentID == "" || s.callbacks.OnToolResult == nil {
		return
	}
	s.callbacks.OnToolResult(ToolResult{Name: name, Arguments: args, AgentID: agentID})
}

// emitToolStart tells the UI that an approved root-agent call is about to
// run. A sub-agent's call is not announced here (emitSubAgentToolLine covers
// it), and a denied call never runs.
func (s *Session) emitToolStart(approved bool, agentID, name, args string) {
	if !approved || agentID != "" || s.callbacks.OnToolStart == nil {
		return
	}
	s.callbacks.OnToolStart(ToolStart{Name: name, Arguments: args})
}

func (s *Session) decideToolCall(req ToolCallRequest) cogito.ToolCallDecision {
	req.ExternalSources = s.activeExternalSourceIDs()
	// Once external data has entered the conversation, consequential actions
	// need a fresh human decision unless session-wide auto-approval is active.
	// Turn-wide and narrower grants cannot silently widen trust granted by
	// external text. PreToolUse hooks still run in yolo mode below.
	externallyInfluenced := len(req.ExternalSources) > 0 &&
		!IsReadOnly(req.Name, req.Arguments, s.readOnlyCommands)
	if externallyInfluenced && !s.autoApprove.Load() {
		note := fmt.Sprintf("Security: this action follows %d untrusted external source(s); review it independently. Turn-wide and narrower grants do not bypass this boundary.", len(req.ExternalSources))
		if req.Reasoning != "" {
			req.Reasoning = note + "\nModel rationale: " + req.Reasoning
		} else {
			req.Reasoning = note
		}
		if s.callbacks.OnToolCall == nil {
			return cogito.ToolCallDecision{Approved: false, Adjustment: "external content influenced a consequential tool call; explicit approval is required"}
		}
		resp := s.callbacks.OnToolCall(req)
		return cogito.ToolCallDecision{Approved: resp.Approved, Adjustment: resp.Adjustment}
	}

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

	if s.autoApprove.Load() || s.allowAllTurn {
		return cogito.ToolCallDecision{Approved: true}
	}
	if s.allowedTools[req.Name] {
		return cogito.ToolCallDecision{Approved: true}
	}
	if req.Name == "bash" {
		if p, ok := BashGrantPrefix(req.Arguments); ok && s.allowedBashPrefixes[p] {
			return cogito.ToolCallDecision{Approved: true}
		}
	}
	// In the default prompt mode, auto-approve calls that only observe state.
	// Not applied in allowlist (explicitly restrictive), strict (prompt for
	// everything), or auto (already approved above). Hooks above still win.
	// classify mode is prompt mode with a classifier in front of the prompt.
	mode := s.currentApprovalMode().OrDefault()
	if (mode == types.ApprovalPrompt || mode == types.ApprovalClassify) &&
		IsReadOnly(req.Name, req.Arguments, s.readOnlyCommands) {
		return cogito.ToolCallDecision{Approved: true}
	}
	// The classifier can only spare the user a prompt: a call it does not
	// approve, or cannot judge, is asked about as usual, with its verdict.
	if st := s.classifier(); mode == types.ApprovalClassify && st != nil {
		v := st.approver.Judge(s.ctx, req)
		if v.Approved {
			if s.callbacks.OnAutoApproved != nil {
				s.callbacks.OnAutoApproved(req, v)
			}
			return cogito.ToolCallDecision{Approved: true}
		}
		req.Verdict = v.String()
	}
	if s.callbacks.OnToolCall == nil {
		return cogito.ToolCallDecision{Approved: true}
	}
	resp := s.callbacks.OnToolCall(req)
	if resp.Approved && resp.AllowAllTurn {
		s.allowAllTurn = true
	}
	if resp.Approved && resp.AlwaysAllow {
		switch {
		case resp.AlwaysPrefix == "":
			s.allowedTools[req.Name] = true
		case req.Name == "bash":
			// Mint a prefix grant only when the approved request itself
			// derives that prefix — the response string is presentation-only
			// and cannot widen the grant. A non-bash request or a mismatched
			// prefix mints nothing at all: the call stays approved, but we
			// deliberately do not fall back to a whole-tool grant the user
			// never saw offered.
			if p, ok := BashGrantPrefix(req.Arguments); ok && p == resp.AlwaysPrefix {
				if s.allowedBashPrefixes == nil {
					s.allowedBashPrefixes = make(map[string]bool)
				}
				s.allowedBashPrefixes[p] = true
			}
		}
	}
	return cogito.ToolCallDecision{Approved: resp.Approved, Adjustment: resp.Adjustment}
}

func (s *Session) isExternalResultTool(name string) bool {
	if strings.HasPrefix(name, "web_") || strings.HasPrefix(name, "browser_") {
		return true
	}
	s.provenanceMu.Lock()
	defer s.provenanceMu.Unlock()
	return s.externalToolNames[name]
}

func (s *Session) recordExternalResult(name, result string) {
	if s.provenanceClassifier == nil || !s.isExternalResultTool(name) || strings.TrimSpace(result) == "" {
		return
	}
	e := provenance.NewExternal(s.ctx, "", "tool-result", name, result, s.provenanceClassifier)
	s.recordExternalEnvelope(e)
}

func (s *Session) recordExternalEnvelope(e provenance.Envelope) {
	s.provenanceMu.Lock()
	if s.externalSources == nil {
		s.externalSources = make(map[string]provenance.Envelope)
	}
	s.externalSources[e.ID] = e
	s.provenanceMu.Unlock()
}

func (s *Session) activeExternalSourceIDs() []string {
	s.provenanceMu.Lock()
	defer s.provenanceMu.Unlock()
	ids := make([]string, 0, len(s.externalSources))
	for id := range s.externalSources {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
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

// StopAgents cancels every sub-agent, running or detached. Cancelling one that
// already finished does nothing.
//
// It is for quitting. Detached sub-agents run on a context that a turn's
// cancel does not reach, so without it they stopped only when the process
// exited, mid-call.
func (s *Session) StopAgents() {
	if s.agentManager == nil {
		return
	}
	for _, a := range s.agentManager.List() {
		if a.Cancel != nil {
			a.Cancel()
		}
	}
}

// emitAgentEvent maps a cogito sub-agent state into a chat.AgentEvent and
// forwards it to the registered OnAgentEvent callback (if any). It is shared by
// the spawn (Status=running) and completion callbacks so the mapping lives in
// one place. s.callbacks.OnAgentEvent is set once in NewSession and never
// reassigned, so reading it from cogito's spawn goroutines is safe.
func (s *Session) emitAgentEvent(a *cogito.AgentState) {
	ev := AgentEvent{
		ID:     a.ID,
		Type:   a.Type,
		Task:   a.Task,
		Status: AgentStatus(a.Status),
		Result: a.Result,
		Err:    a.Error,
	}
	switch ev.Status {
	case AgentStatusRunning:
		s.agentMu.Lock()
		if s.agentStart == nil {
			s.agentStart = make(map[string]time.Time)
		}
		s.agentStart[a.ID] = time.Now()
		s.agentMu.Unlock()
		if s.agentLogs != nil {
			s.agentLogs.started(a.ID, time.Now())
		}
	case AgentStatusCompleted, AgentStatusFailed:
		if s.agentLogs != nil {
			s.agentLogs.forget(a.ID)
		}
		var au cogito.LLMUsage
		ev.ToolCount, au = agentUsageFull(a)
		ev.TotalTokens = au.TotalTokens
		ev.OutputTokens = au.CompletionTokens
		// Sub-agent tokens are spent on the same bill as the main loop, so the
		// session total owns them too.
		//
		// Counting here rather than in the main loop is what makes this correct
		// exactly once: cogito hands sub-agents the UNWRAPPED parent LLM (see
		// prepareAgentTools, which captures agentLLM before ExecuteTools wraps
		// it in a counting LLM), so a sub-agent's spend never lands in the
		// parent run's CumulativeUsage that SendMessage adds. Without this the
		// tokens are invisible; adding it in both places would double them.
		//
		// The failed branch is counted deliberately, but today it contributes
		// exactly zero, and this is the one place that is easy to misread:
		// cogito assigns agent.Fragment only on the success branch and drops
		// the fragment on error, so for a real failure agentUsageFull reads
		// through a nil fragment and returns an empty LLMUsage (which is why
		// its doc says a failed agent never gets one). The call stays because
		// the intent is honest — tokens burned before a failure were still
		// billed — and it starts reporting the moment cogito preserves a failed
		// agent's fragment, with no change needed here. Until then a failed
		// sub-agent's spend is under-reported, which is the safe direction for
		// a figure a benchmark harness reads: too low never invents spend.
		s.addUsage(au)
		s.agentMu.Lock()
		if start, ok := s.agentStart[a.ID]; ok {
			ev.Elapsed = time.Since(start)
			delete(s.agentStart, a.ID)
		}
		s.agentMu.Unlock()
	}
	if s.callbacks.OnAgentEvent != nil {
		s.callbacks.OnAgentEvent(ev)
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

// agentUsageFull extracts a finished sub-agent's executed-tool count and full
// cumulative token usage. Safe against a nil agent, fragment or status — a
// failed agent never gets a fragment.
//
// The full LLMUsage (not just the total) exists because the session counter
// tracks prompt and completion tokens separately: folding a sub-agent in with
// only its total would leave the two component figures silently short of it.
func agentUsageFull(a *cogito.AgentState) (toolCount int, usage cogito.LLMUsage) {
	if a == nil || a.Fragment == nil || a.Fragment.Status == nil {
		return 0, cogito.LLMUsage{}
	}
	return len(a.Fragment.Status.ToolsCalled), a.Fragment.Status.CumulativeUsage
}

func (s *Session) ClearHistory() {
	s.historyMu.Lock()
	s.messages = []openai.ChatCompletionMessage{}
	s.fragment = cogito.NewEmptyFragment()
	s.historyMu.Unlock()
}

// ExportHistory returns a copy of the full conversation messages (the same
// []openai.ChatCompletionMessage that backs the model context), suitable for
// JSON serialization and persistence. Feed the result back via
// types.Config.InitialHistory to resume the conversation losslessly — the model
// then continues with real memory of it, not a summary.
//
// The returned slice is a copy, so mutating it (or its serialization) never
// touches the live session. It EXCLUDES the system prompt: s.messages only ever
// records user/assistant turns (the system prompt is regenerated per
// model/locale and re-applied to the fragment on every turn), so there is no
// system message to strip. Safe to call from another goroutine while a turn is
// running; it takes the same lock SendMessage holds while appending.
func (s *Session) ExportHistory() []openai.ChatCompletionMessage {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	return slices.Clone(s.messages)
}

// SetGoal sets (or replaces) the active session goal. While a goal is set, a
// turn re-runs until the model calls goal_done or the user interrupts. Call
// between turns, not during a live run: the goal_done tool is wired at the
// start of a turn, so arming a goal mid-run would not expose goal_done.
func (s *Session) SetGoal(goal string) {
	s.runMu.Lock()
	s.goal = goal
	s.goalPaused = false
	s.runMu.Unlock()
}

// Goal returns the session goal, or "" if none. A paused goal is still
// returned; see GoalPaused.
func (s *Session) Goal() string {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.goal
}

// ClearGoal removes the session goal, paused or not.
func (s *Session) ClearGoal() {
	s.runMu.Lock()
	s.goal = ""
	s.goalPaused = false
	s.runMu.Unlock()
}

// PauseGoal stops pursuing the goal but keeps its text. Turns run as if no
// goal were set until ResumeGoal. It does nothing when there is no goal.
func (s *Session) PauseGoal() {
	s.runMu.Lock()
	s.goalPaused = s.goal != ""
	s.runMu.Unlock()
}

// ResumeGoal pursues a paused goal again from the next turn. It reports
// whether there was a paused goal to resume.
func (s *Session) ResumeGoal() bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if s.goal == "" || !s.goalPaused {
		return false
	}
	s.goalPaused = false
	return true
}

// GoalPaused reports whether the goal is paused.
func (s *Session) GoalPaused() bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.goalPaused
}

// activeGoalLocked returns the goal a turn pursues: "" when there is none or
// it is paused. The caller holds runMu.
func (s *Session) activeGoalLocked() string {
	if s.goalPaused {
		return ""
	}
	return s.goal
}

// activeGoal is activeGoalLocked for callers that do not hold runMu.
func (s *Session) activeGoal() string {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.activeGoalLocked()
}

// TodoList returns the session's ephemeral todo list (may be nil if the session
// was constructed without one, though in practice it is always set).
func (s *Session) TodoList() *TodoList {
	return s.todoList
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

// SetShellJobs wires the shared shell-job registry into the session so the
// pending-work predicate keeps a run parked while a backgrounded shell command
// is still running, and so finished shell jobs inject a completion notice into
// the live run. Registers the completion hook. Call once at setup.
func (s *Session) SetShellJobs(jobs *wizmcp.ShellJobs) {
	s.shellJobs = jobs
	if jobs == nil {
		return
	}
	jobs.SetOnJobDone(func(info wizmcp.ShellJobInfo) {
		// Only backgrounded jobs interest the live run: a plain foreground
		// command is consumed inline by the tool call that started it.
		if !info.Backgrounded {
			return
		}
		notice := "shell job " + info.ID + " " + info.Status
		if so, se, ok := jobs.Output(info.ID); ok {
			if tail := jobTail(so + se); tail != "" {
				notice += ":\n" + tail
			}
		}
		s.deliverNotice(notice)
	})
}

// maxPendingNotices caps the notices kept for the next turn. A burst of jobs
// finishing while the user is away must not flood the next prompt; the newest
// notices are the ones kept.
const maxPendingNotices = 16

// deliverNotice injects a background-job notice into the live run, or keeps
// it for the next turn when no run is live.
//
// Without the second half a job that finished after an interrupt ended the
// run, or between turns, reached the model never: Inject had no run to
// deliver to and dropped it.
func (s *Session) deliverNotice(notice string) {
	if s.Inject(notice) {
		return
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.pendingNotices = append(s.pendingNotices, notice)
	if n := len(s.pendingNotices); n > maxPendingNotices {
		s.pendingNotices = slices.Clone(s.pendingNotices[n-maxPendingNotices:])
	}
}

// takePendingNotices returns and clears the notices kept for the next turn.
func (s *Session) takePendingNotices() []string {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	out := s.pendingNotices
	s.pendingNotices = nil
	return out
}

// restorePendingNotices puts notices back ahead of any that arrived since, for
// a turn that failed before the model saw them.
func (s *Session) restorePendingNotices(notices []string) {
	if len(notices) == 0 {
		return
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.pendingNotices = append(slices.Clone(notices), s.pendingNotices...)
}

// pendingNoticesMessage renders kept notices as the one message that goes
// before the user's message in the next turn.
func pendingNoticesMessage(notices []string) string {
	return "Background jobs finished while no turn was running:\n\n" + strings.Join(notices, "\n\n")
}

// InjectUser delivers a user-typed follow-up into the live run (see Inject),
// and additionally tracks it: if the run returns before consuming it (the
// model never saw it), the text is reported by TakeUndelivered so the caller
// can re-dispatch it as a fresh turn instead of losing it to the end-of-run
// drain. System notices (shell-job completions, wake-ups) keep using Inject —
// re-running a stale notice as a fresh turn would re-trigger finished work.
func (s *Session) InjectUser(msg string) bool {
	s.runMu.Lock()
	s.userInjected = append(s.userInjected, msg)
	s.runMu.Unlock()
	if s.Inject(msg) {
		return true
	}
	// Nothing was sent: untrack so the drain can't misreport it later.
	s.runMu.Lock()
	if i := slices.Index(s.userInjected, msg); i >= 0 {
		s.userInjected = slices.Delete(s.userInjected, i, i+1)
	}
	s.runMu.Unlock()
	return false
}

// TakeUndelivered returns (and clears) user-typed follow-ups that were
// injected into a run that ended before consuming them. Call after a run
// returns to re-dispatch them as fresh turns.
func (s *Session) TakeUndelivered() []string {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	out := s.undelivered
	s.undelivered = nil
	return out
}

// Inject delivers msg into the live run's message-injection channel, waking a
// parked loop so the assistant continues in the SAME run. Non-blocking: returns
// false when there is no live run, the channel is full, or msg is empty. Used
// for mid-run user follow-ups, shell-job completions, and scheduled wake-ups.
func (s *Session) Inject(msg string) bool {
	if strings.TrimSpace(msg) == "" {
		return false
	}
	s.runMu.Lock()
	live := s.runLive
	s.runMu.Unlock()
	if !live {
		return false
	}
	select {
	case s.inject <- openai.ChatCompletionMessage{Role: "user", Content: msg}:
		return true
	default:
		return false
	}
}

// RunLive reports whether a run is currently in flight (between SendMessage
// start and return), including while it is parked. The TUI uses this as the
// authoritative signal for whether a typed message should be queued into the
// live run or start a new turn, rather than its own loading/parked UI flags
// which can briefly desync across park/resume events.
func (s *Session) RunLive() bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.runLive
}

// jobTail returns the trailing portion of a job's output for an injection
// notice, trimmed and capped.
func jobTail(s string) string {
	const limit = 600
	s = strings.TrimSpace(s)
	if len(s) > limit {
		s = "…" + s[len(s)-limit:]
	}
	return s
}

// toolEnabled reports whether a built-in tool is exposed to the model. With an
// empty BuiltinTools allowlist (the default) every tool is exposed; otherwise only the
// named ones. Approval gating is separate (see allowedTools).
func (s *Session) toolEnabled(name string) bool {
	return len(s.toolAllow) == 0 || s.toolAllow[name]
}

// mcpToolFilter returns the filter cogito uses to gate MCP-sourced tool calls.
// Tools from a user-configured MCP server (s.cfgClients) always pass — the
// allowlist restricts nib's own built-in and self-config tools, never a
// server the user explicitly added. Everything else falls back to toolEnabled.
//
// This MUST be rebuilt fresh on every call (it already is — called once per
// SendMessage). Never cache/memoize the returned closure or the cfgSessions map
// across turns: ReconcileMCPServers Close()s and replaces session pointers on
// reconnect/reconfigure, so a cached map would hold stale pointers, fail the
// identity check for the new sessions, and silently fall back to toolEnabled —
// reintroducing exactly the bug this method fixes.
func (s *Session) mcpToolFilter() func(*mcp.ClientSession, string) bool {
	cfgSessions := make(map[*mcp.ClientSession]bool, len(s.cfgClients))
	for _, sess := range s.cfgClients {
		cfgSessions[sess] = true
	}
	return func(sess *mcp.ClientSession, name string) bool {
		if cfgSessions[sess] {
			s.provenanceMu.Lock()
			if s.externalToolNames == nil {
				s.externalToolNames = make(map[string]bool)
			}
			s.externalToolNames[name] = true
			s.provenanceMu.Unlock()
			return true
		}
		// search_artifacts is only useful when the artifact store has content.
		if name == "search_artifacts" {
			return s.toolEnabled(name) && s.artifacts != nil && s.artifacts.Count() > 0
		}
		return s.toolEnabled(name)
	}
}

// fragmentTokens is the byte/4 estimate of the conversation, read under the
// history lock.
func (s *Session) fragmentTokens() int {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	return estimateTokens(s.fragment.Messages)
}

// noticesText is the message pendingNoticesMessage made for notices, or ""
// when there were none.
func noticesText(notices []string) string {
	if len(notices) == 0 {
		return ""
	}
	return pendingNoticesMessage(notices)
}

// dropFailedTurn removes a failed turn's own messages from msgs, a history
// that overflow recovery compacted during the turn, and keeps the compaction.
//
// The turn's messages start at its user message (and the notices message
// just before it, which the caller hands back to the pending list). When the
// user message is still in msgs, everything from it on goes. When it is not,
// the compaction's summary covers it, and every message after the summary
// (the first non-system message) is the turn's own: the kept tail is a suffix
// of the history, and the user message came before all of it. System messages
// are kept wherever they are, since ensureSystemPrompt may have appended the
// prompt after the tail.
//
// It reports false when the result would break the tool pairing; the caller
// then falls back to the pre-turn history.
func dropFailedTurn(msgs []openai.ChatCompletionMessage, user openai.ChatCompletionMessage, notices string) ([]openai.ChatCompletionMessage, bool) {
	cut := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == user.Role && m.Content == user.Content && len(m.MultiContent) == len(user.MultiContent) {
			cut = i
			break
		}
	}
	if cut >= 0 {
		if notices != "" && cut > 0 && msgs[cut-1].Role == "user" && msgs[cut-1].Content == notices {
			cut--
		}
	} else {
		cut = len(msgs)
		for i, m := range msgs {
			if m.Role != "system" {
				cut = i + 1
				break
			}
		}
	}
	kept := append([]openai.ChatCompletionMessage(nil), msgs[:cut]...)
	for _, m := range msgs[cut:] {
		if m.Role == "system" {
			kept = append(kept, m)
		}
	}
	if len(kept) == 0 || validateToolPairing(kept) != nil {
		return nil, false
	}
	return kept, true
}

// dropFailedDisplay removes a failed turn's user message, and anything after
// it, from the display copy, as the pre-turn rollback does.
func dropFailedDisplay(msgs []openai.ChatCompletionMessage, text string) []openai.ChatCompletionMessage {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" && msgs[i].Content == text {
			return msgs[:i]
		}
	}
	return msgs
}

// overflowTrim is the first step of overflow recovery. It is a variable so a
// test can make it fail and reach the basicCompact fallback.
var overflowTrim = func(s *Session, ctx context.Context) error { return s.iterativeTrim(ctx) }

// buildUserFragment appends the user turn to the fragment, attaching multimodal
// parts. cogito's AddMessage routes image parts into image_url MultiContent and
// audio/video into the fragment's transient PendingNativeParts (send-once).
func buildUserFragment(f cogito.Fragment, text string, parts []ContentPart) cogito.Fragment {
	if len(parts) == 0 {
		return f.AddMessage("user", text)
	}
	mm := make([]cogito.Multimedia, 0, len(parts))
	for _, p := range parts {
		mm = append(mm, p) // ContentPart implements cogito.TypedMultimedia
	}
	return f.AddMessage("user", text, mm...)
}

// toolOptions returns the cogito options that shape the tool schemas a run
// advertises: the MCP sessions and their filter, the built-in tools (each gated
// by the BuiltinTools allowlist via toolEnabled), the media tools, the goal
// tool, the self-config tools, agent spawning, and the sink-state setting
// (cogito appends its own "reply" tool unless it is disabled).
//
// SendMessage and Warm both call this so a priming request advertises exactly
// the tools a real turn advertises. The tool schemas are serialized into the
// prompt by the server's chat template, so any difference here changes the
// cached prefix and silently wastes the prime.
//
// goal is passed in rather than read via s.Goal(): Goal() takes runMu, and Warm
// reads the goal under runMu before releasing it to make the long-running
// Prefill call. Reading it here instead would either re-take a held lock or
// force Warm to hold runMu across the network call, blocking Interrupt.
//
// mainModel is passed in for the same reason plus one of its own: the caller
// snapshots it alongside the LLM client it will run with (see currentLLM), so
// the sub-agents a turn spawns resolve against the same model the turn itself
// is using, even if SetModel lands halfway through.
func (s *Session) toolOptions(turnCtx context.Context, goal, mainModel string) []cogito.Option {
	s.resetSchemaTools()
	opts := []cogito.Option{
		cogito.WithMCPs(s.allClients()...),
		// Disable cogito's sink-state "reply" tool so ExecuteTools is the whole
		// turn: when the LLM stops calling tools it records its text reply as the
		// final answer (read via LastMessage below). This matches cogito's own
		// examples (examples/chat, examples/sub-agents) and avoids a redundant
		// follow-up Ask that returns empty on many models. This also removes a
		// tool from the advertised set, so it belongs here and not with the
		// run-behavior options.
		cogito.DisableSinkState,
		cogito.WithAgentDefinitions(s.agentDefs...),
		cogito.WithAgentLLMFactory(func(model string, temperature float32, metadata map[string]string) cogito.LLM {
			return s.newAgentLLM(mainModel, model, temperature, metadata)
		}),
	}

	// Built-in tools, each gated by the BuiltinTools allowlist (toolEnabled). The
	// agent-spawning tools (spawn_agent/check_agent/get_agent_result) ride
	// EnableAgentSpawning; the rest are individual registrations. The agent
	// manager/factory stay wired regardless — harmless without the tools.
	if s.toolEnabled("spawn_agent") {
		opts = append(opts, cogito.EnableAgentSpawning)
	}
	if s.toolEnabled("ask_user") && !s.AutoApprove() {
		opts = append(opts, s.withTool(askUserToolDefinition(func(req AskRequest) string {
			if s.callbacks.OnAskUser != nil {
				return s.callbacks.OnAskUser(req)
			}
			return ""
		})))
	}
	if s.toolEnabled("agent_logs") {
		opts = append(opts, s.withTool(agentLogsToolDefinition(s.AgentLog)))
	}
	if s.toolEnabled("schedule_wakeup") {
		opts = append(opts, s.withTool(scheduleWakeupToolDefinition(func(req WakeupRequest) string {
			if s.callbacks.OnScheduleWakeup != nil {
				return s.callbacks.OnScheduleWakeup(req)
			}
			return "Scheduling is not available in this session."
		}, func() bool {
			return s.agentManager.HasRunning() || (s.shellJobs != nil && s.shellJobs.HasRunning())
		})))
	}
	if s.toolEnabled("cron") {
		opts = append(opts, s.withTool(cronToolDefinition(func(req CronRequest) string {
			if s.callbacks.OnCronCreate != nil {
				return s.callbacks.OnCronCreate(req)
			}
			return "Scheduling is not available in this session."
		})))
	}
	if s.toolEnabled("cron_list") {
		opts = append(opts, s.withTool(cronListToolDefinition(func() string {
			if s.callbacks.OnCronList != nil {
				return s.callbacks.OnCronList()
			}
			return "No scheduler available."
		})))
	}
	if s.toolEnabled("cron_delete") {
		opts = append(opts, s.withTool(cronDeleteToolDefinition(func(id string) string {
			if s.callbacks.OnCronDelete != nil {
				return s.callbacks.OnCronDelete(id)
			}
			return "No scheduler available."
		})))
	}
	if s.toolEnabled("cron_pause") {
		opts = append(opts, s.withTool(cronPauseToolDefinition(func(id string) string {
			if s.callbacks.OnCronPause != nil {
				return s.callbacks.OnCronPause(id)
			}
			return "No scheduler available."
		})))
	}
	if s.toolEnabled("cron_resume") {
		opts = append(opts, s.withTool(cronResumeToolDefinition(func(id string) string {
			if s.callbacks.OnCronResume != nil {
				return s.callbacks.OnCronResume(id)
			}
			return "No scheduler available."
		})))
	}
	if s.toolEnabled("cron_trigger") {
		opts = append(opts, s.withTool(cronTriggerToolDefinition(func(id string) string {
			if s.callbacks.OnCronTrigger != nil {
				return s.callbacks.OnCronTrigger(id)
			}
			return "No scheduler available."
		})))
	}

	// Media understanding tools, gated by the allowlist. Each delegates to a
	// specialist client with the tool's dedicated model and scopes the path to
	// the session working dir, mirroring how host file tools resolve paths.
	if s.toolEnabled("read_image") {
		opts = append(opts, s.withTool(readImageToolDefinition(
			func(path, question string) (string, error) {
				return specialist.New(s.baseURL, s.apiKey).Describe(
					turnCtx, resolveWorkspacePath(s.workingDir, path), s.visionModel, question)
			})))
	}
	if s.toolEnabled("transcribe_audio") {
		opts = append(opts, s.withTool(transcribeAudioToolDefinition(
			func(path string) (string, error) {
				return specialist.New(s.baseURL, s.apiKey).Transcribe(
					turnCtx, resolveWorkspacePath(s.workingDir, path), s.transcribeModel)
			})))
	}
	if s.toolEnabled("read_video") {
		opts = append(opts, s.withTool(readVideoToolDefinition(
			func(path, question string) (string, error) {
				return specialist.New(s.baseURL, s.apiKey).DescribeVideo(
					turnCtx, resolveWorkspacePath(s.workingDir, path), s.videoModel, question)
			})))
	}

	// Register the goal_done tool only while a goal is active, so it never
	// appears as a no-op tool in ordinary turns. The callback records
	// completion: it sets the per-run flag (read by the stop-gate) and clears
	// the goal so it does not re-arm on the next message. The callback body takes
	// runMu, which is correct: it fires during a run, not during option assembly.
	if goal != "" {
		opts = append(opts, s.withTool(goalDoneToolDefinition(func(justification string) string {
			s.runMu.Lock()
			s.goalDone = true
			s.goal = ""
			s.goalPaused = false
			s.runMu.Unlock()
			return "Goal marked complete: " + justification
		})))
	}

	// Wire the persistent memory tool so the assistant can save and retrieve
	// notes that survive compaction and model restarts.
	if s.toolEnabled("memory") {
		opts = append(opts, s.withTool(memoryToolDefinition(s.memoryStore)))
	}

	// Wire the ephemeral todo list so the assistant can plan and track
	// multi-step work within the current session (replace-all semantics,
	// like maki).
	if s.toolEnabled("todo_write") {
		opts = append(opts, s.withTool(todoWriteToolDefinition(s.todoList)))
	}

	// Wire the tree-sitter index tool so the assistant can skeletonize source
	// files — a compact structural overview before deciding what to read.
	if s.toolEnabled("index") {
		opts = append(opts, s.withTool(indexToolDefinition(
			func(p string) string { return resolveWorkspacePath(s.workingDir, p) })))
	}

	// Wire the repo_map tool so the assistant can get a bird's-eye overview of
	// the whole codebase in one token-budgeted call.
	if s.toolEnabled("repo_map") {
		opts = append(opts, s.withTool(repoMapToolDefinition(
			s.workingDir,
			func(p string) string { return resolveWorkspacePath(s.workingDir, p) },
		)))
	}

	// Wire the LSP tool so the assistant can do symbol-aware navigation
	// (definition, references, symbols) when a language server is configured.
	if s.lspManager != nil && s.toolEnabled("lsp") {
		opts = append(opts, s.withTool(lspToolDefinition(
			s.lspManager,
			func(p string) string { return resolveWorkspacePath(s.workingDir, p) },
		)))
	}

	// Wire the native self-configuration tools so the assistant can manage its
	// own plugins, skills, and MCP servers. requestReload re-wires the live
	// session on the next turn after any mutating op.
	for _, d := range selfConfigToolDefs(s.configurator, s.requestReload) {
		if s.toolEnabled(d.name) {
			opts = append(opts, s.withTool(d.def))
		}
	}

	// Restrict built-in host tools (read/write/edit/bash/glob/grep/web_*) to the
	// allowlist too, but never MCP servers the user explicitly configured. The
	// filter is also the point where configured MCP tool names are registered as
	// external provenance sources, so install it even when the allowlist is empty.
	opts = append(opts, cogito.WithMCPToolFilter(s.mcpToolFilter()))

	return opts
}

func (s *Session) SendMessage(text string, parts ...ContentPart) (string, error) {
	if s.hooks != nil {
		s.hooks.Fire(s.ctx, hooks.EventUserPromptSubmit, "", map[string]any{"event": "UserPromptSubmit", "prompt": text})
	}
	turnCtx := s.beginTurn()
	// Report stalled sub-agents while this turn runs; notices can only reach
	// a live run. See stale.go.
	staleCtx, stopStale := context.WithCancel(turnCtx)
	defer stopStale()
	go s.watchStaleAgents(staleCtx)
	s.applyPendingReload()
	// The endpoint is asked for this model's context window and output cap
	// here, on the first turn that uses it, rather than while the session or
	// the client was being built.
	s.ensureModelLimits(turnCtx)
	s.allowAllTurn = false
	// The overflow retry is capped per TURN, not per session: a later turn that
	// overflows deserves its own recovery attempt.
	s.overflowMu.Lock()
	s.overflowRetried = 0
	s.overflowMu.Unlock()
	// A budget overflow lowers the output reservation for one turn only.
	s.setTurnOutputCap(0)
	defer s.setTurnOutputCap(0)
	s.turnRetryMu.Lock()
	s.turnRetryTotal = 0
	s.turnRetryMu.Unlock()
	// Failed attempts in a row that made no progress; see turnRetryBudget.
	stalled := 0
	// Output-cap and budget overflows are retried at most once per turn each;
	// neither compacts, so neither counts as an overflow recovery.
	capRetried, budgetRetried := false, false
	defer s.endTurn()
	// Report this turn's own size while it runs; hand authority back to
	// s.fragment (which compaction may since have shrunk) once it ends.
	s.live.begin()
	defer s.live.end()

	// Mark the run live so Inject can target it; clear on return. Drain the
	// injection channel so stale entries can't leak into the next run — but
	// user-typed follow-ups (InjectUser) still sitting there were never seen
	// by the model: hand those back via TakeUndelivered instead of silently
	// discarding them with the system notices.
	s.runMu.Lock()
	s.runLive = true
	s.runMu.Unlock()
	defer func() {
		s.runMu.Lock()
		s.runLive = false
		pendingUser := s.userInjected
		s.userInjected = nil
		s.runMu.Unlock()
		var undelivered []string
		for {
			select {
			case msg := <-s.inject:
				if i := slices.Index(pendingUser, msg.Content); i >= 0 {
					pendingUser = slices.Delete(pendingUser, i, i+1)
					undelivered = append(undelivered, msg.Content)
				}
			default:
				s.runMu.Lock()
				s.undelivered = append(s.undelivered, undelivered...)
				s.runMu.Unlock()
				return
			}
		}
	}()
	s.ensureSystemPrompt()

	s.historyMu.Lock()
	// Snapshot the history before committing the user message, so a failed or
	// interrupted turn can be rolled back to the pre-turn state. Without this,
	// the user's message is orphaned (committed with no assistant reply) and a
	// retry double-adds it.
	preTurnFragment := s.fragment
	preTurnMessages := s.messages
	// Notices kept from while no run was live go first, so the model reads
	// them before the message that may ask about them. They are not added to
	// s.messages: the user never typed them.
	notices := s.takePendingNotices()
	if len(notices) > 0 {
		s.fragment = s.fragment.AddMessage("user", pendingNoticesMessage(notices))
	}
	s.fragment = buildUserFragment(s.fragment, text, parts)
	// The turn's own message, so a failed turn can be cut out of a history
	// that overflow recovery has since compacted (see dropFailedTurn).
	turnUser := s.fragment.Messages[len(s.fragment.Messages)-1]
	s.messages = append(s.messages, openai.ChatCompletionMessage{
		Role:    "user",
		Content: text,
	})
	s.historyMu.Unlock()

	// Post-compaction context-file nudge: when end-of-turn compaction
	// succeeded on the previous turn, contextNudgePending is set. Inject a
	// one-time user-role reminder to re-read detected project instruction
	// files (AGENTS.md, CLAUDE.md, etc.) before the model starts tool-calling.
	// The tool call proceeds regardless — this is a soft nudge, not a gate.
	// If the model already re-read the file in the previous turn, the flag
	// was cleared by the read-tracking in the tool-call callback, and no
	// nudge fires.
	s.maybeInjectContextNudge()

	// Snapshot the client and its model once for the whole turn: SetModel can
	// swap them from another goroutine at any point in here (turnMu does not
	// hold a turn), and a turn that changed model halfway would send its
	// remaining requests to a different model than the one it started on.
	llm, mainModel := s.currentLLM()
	// Every request this turn makes reports its prompt size through the
	// wrapper, which is the only race-free place to observe it: cogito writes
	// LastUsage onto a Status the UI goroutine must not read (see liveUsage).
	// Sub-agent and reviewer clients are deliberately left unwrapped — their
	// spend is not this conversation's context size. Sub-agents get their own
	// retrying client instead (WithAgentLLM below; see agentretry.go):
	// without it cogito would hand them this tracked one.
	agentLLM := retryForAgent(llm, &s.agentBackoff)
	llm = trackUsage(llm, &s.live, s.requestLimits)

	// Build cogito options from config
	cogitoOpts := []cogito.Option{
		cogito.WithAgentLLM(agentLLM),
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
		// Feed tool-returned images (e.g. computer_use screenshots) back to the
		// model only when desktop control is armed for this session.
		cogito.WithToolImageForwarding(s.computerEnabled),
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
			// Track reads of project instruction files (AGENTS.md, etc.).
			// Once the model has re-read any detected context file after
			// compaction, the post-compaction nudge is no longer needed.
			s.trackContextFileRead(tool.Name, string(args))
			change := PreviewFileChange(s.workingDir, tool.Name, string(args))
			decision := s.decideToolCall(ToolCallRequest{
				Name:      tool.Name,
				Arguments: string(args),
				Reasoning: tool.Reasoning,
				AgentID:   state.AgentID,
				Change:    change,
			})
			if decision.Approved && state.AgentID == "" && s.callbacks.OnToolResult != nil {
				s.changes.put(changeKey(tool.ID, tool.Name, string(args)), change)
			}
			s.emitSubAgentToolLine(decision.Approved, state.AgentID, tool.Name, string(args))
			s.emitToolStart(decision.Approved, state.AgentID, tool.Name, string(args))
			return decision
		}),
		cogito.WithToolCallResultCallback(func(status cogito.ToolStatus) {
			s.agentLogs.recordResult(status) // no-op for root-agent tool calls
			s.recordExternalResult(status.Name, status.Result)
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
				change := s.changes.take(changeKey(status.ToolArguments.ID, status.Name, argsJSON))
				if change != nil {
					if failed, _ := ToolOutcome(status.Result); failed {
						change = nil
					} else {
						change.settle(s.workingDir)
					}
				}
				s.callbacks.OnToolResult(ToolResult{
					Name:      status.Name,
					Result:    status.Result,
					Arguments: argsJSON,
					AgentID:   s.agentLogs.agentFor(status.ToolArguments.ID),
					Change:    change,
					Images:    extractToolImages(status.ResultData),
				})
			}
		}),
		// Park/inject/resume: keep the run alive (parked on the injection channel)
		// whenever a sub-agent or a backgrounded shell job is still running, and
		// let the user inject mid-run follow-ups. cogito auto-injects sub-agent
		// completion results; shell-job completions and wake-ups inject via s.inject.
		cogito.WithMessageInjectionChan(s.inject),
		cogito.WithPendingWork(func() bool {
			return s.agentManager.HasRunning() || s.shellJobs.HasRunning()
		}),
		cogito.WithOnPark(func(reply string) {
			// cogito hands us the no-tool reply text recorded in the fragment
			// right before the loop blocked — the parked reply the UI surfaces.
			if s.callbacks.OnParked != nil {
				s.callbacks.OnParked(reply)
			}
		}),
		cogito.WithOnResume(func() {
			if s.callbacks.OnResumed != nil {
				s.callbacks.OnResumed()
			}
		}),
	}

	// Step-boundary commentary: the assistant text that accompanied a tool
	// selection ("I'll search for X now…"), delivered before the tools run so
	// a UI can commit it in chronological order relative to OnToolResult.
	if s.callbacks.OnStepContent != nil {
		cogitoOpts = append(cogitoOpts, cogito.WithStepContentCallback(func(content string) {
			s.callbacks.OnStepContent(content)
		}))
	}

	// Live token streaming: opt into cogito's streaming decision/answer path only
	// when a consumer wants per-token deltas. Left unset (e.g. the CLI), the path
	// is identical to before. The step-boundary callbacks still fire either way.
	if s.callbacks.OnStream != nil {
		cogitoOpts = append(cogitoOpts, cogito.WithStreamCallback(func(ev cogito.StreamEvent) {
			s.callbacks.OnStream(StreamEvent{
				Kind:     string(ev.Type),
				Content:  ev.Content,
				ToolName: ev.ToolName,
				ToolArgs: ev.ToolArgs,
				AgentID:  ev.AgentID,
			})
		}))
	}

	// Compacts between tool steps when the turn outgrows the trigger; see
	// turnCompactor. Its summary reaches s.fragment only through commitRun.
	midTurn := s.newTurnCompactor(turnCtx)

	cogitoOpts = append(cogitoOpts,
		// Rewrites what goes on the wire, never s.fragment. Installed here
		// rather than in toolOptions because toolOptions is shared with Warm,
		// whose contract is that a priming request advertises exactly the tool
		// schemas a real turn advertises — a message manipulator is neither.
		cogito.WithMessagesManipulator(midTurn.manipulate),
		cogito.WithAgentManager(s.agentManager),
		cogito.WithAgentSpawnCallback(func(a *cogito.AgentState) {
			s.emitAgentEvent(a)
		}),
		cogito.WithAgentCompletionCallback(func(a *cogito.AgentState) {
			s.emitAgentEvent(a)
		}),
	)

	// Tool schemas — shared with Warm so a priming request advertises exactly
	// the tools this turn advertises. See toolOptions.
	cogitoOpts = append(cogitoOpts, s.toolOptions(turnCtx, s.activeGoal(), mainModel)...)

	// Add ForceReasoning only if enabled in config
	if s.cogitoOptions.ForceReasoning {
		cogitoOpts = append(cogitoOpts, cogito.WithForceReasoning())
	}

	// Run the agent loop. With sink-state disabled, ExecuteTools runs the whole
	// turn and leaves the final natural-language answer as the last message.
	//
	// Stop-gate (/goal): while a goal is active, re-run after each stop until
	// the model calls goal_done (which clears the goal and sets goalDone) or the
	// turn is interrupted. The user can chat/steer mid-pursuit via the existing
	// inject path; Ctrl+C cancels turnCtx and pauses the goal.
	var err error
	var response string
	for {
		s.runMu.Lock()
		s.goalDone = false
		s.runMu.Unlock()

		// ExecuteTools takes the fragment by value, so it operates on a copy
		// of the Messages slice — but Fragment.Status is a *Status pointer,
		// which is shared, not copied. Without the deep copy below, a failed
		// or interrupted turn would leave s.fragment.Status polluted with
		// PastActions, ToolsCalled, Iterations etc. from the failed run,
		// causing false loop detection and incorrect ErrNoToolSelected
		// behaviour on the next turn.
		//
		// Only the reassignment of the result needs the lock (below), not the
		// whole call — holding it across the call would block ExportHistory for
		// the entire turn.
		runFragment := s.fragment
		if runFragment.Status != nil {
			statusCopy := *runFragment.Status
			runFragment.Status = &statusCopy
		}
		var newFragment cogito.Fragment
		midTurn.reset()
		newFragment, err = cogito.ExecuteTools(llm, runFragment, cogitoOpts...)
		// The run's requests measured the tool-schema floor; tell the user
		// once when it takes a large share of the window.
		s.notifySchemaBudget()
		if err != nil && !errors.Is(err, cogito.ErrNoToolSelected) {
			// Interrupt (turnCtx cancelled) surfaces here as a context error;
			// pause the goal so the user's stop sticks and it doesn't re-arm,
			// while keeping its text for /goal resume. Other (transient) errors
			// leave the goal active intentionally.
			if turnCtx.Err() != nil {
				s.PauseGoal()
			}
			// The tokens this run already burned are real and already billed,
			// so record them before bailing. cogito stamps CumulativeUsage in a
			// defer onto the fragment it returns, including on its error paths,
			// so an interrupt that lands after several tool-loop calls arrives
			// here with the whole spend in hand. Dropping it would let a user
			// erase thousands of paid-for tokens by pressing Ctrl+C — in a
			// counter that exists to account for spend, and in the usage.json a
			// benchmark harness reads.
			//
			// The turn deliberately is NOT counted here: the user never got the
			// exchange they asked for. Tokens and turns diverge on this path,
			// which is the point of keeping them separate counters.
			if newFragment.Status != nil {
				s.addUsage(newFragment.Status.CumulativeUsage)
			}

			// Context-overflow recovery. The backend has just told us the
			// model's real window — the one moment it does so reliably — so
			// keep it, then compact and try the turn again.
			//
			// Exactly once. If the overflow comes from the system prompt plus
			// tool schemas alone, or from a tail splitForCompaction must keep
			// intact, the second attempt fails identically and a loop would
			// re-summarise into the same wall, burning tokens every pass.
			//
			// Never after an interrupt: a cancelled turnCtx means the user
			// pressed Ctrl+C, and re-sending is the opposite of what they asked
			// for. See canRecoverFromOverflow.
			//
			// The turn's own classification, not the free isContextOverflow:
			// only the session knows how large the failed request was, which
			// decides whether a 413 with no token wording is an overflow.
			turnOverflow := s.classifyTurnOverflow(err)
			logUnclassifiedRejection(err, turnOverflow)
			announce := func(status string) {
				if s.callbacks.OnStatus != nil {
					s.callbacks.OnStatus(status)
				}
			}
			if turnCtx.Err() == nil {
				switch turnOverflow.Kind {
				case KindOutputCap:
					// The requested output alone is above the model's
					// maximum. Compaction cannot fix that; a lower cap can.
					// Once per turn, and only when the cap really drops, so
					// the retry is not the same request again.
					if !capRetried && s.lowerOutputCap(turnOverflow.Window, mainModel) {
						capRetried = true
						xlog.Warn("output cap above the model's maximum; lowered it and retrying", "cap", turnOverflow.Window)
						announce("Output limit too large — lowering it and retrying…")
						announce(retryResumeStatus)
						continue
					}
				case KindBudget:
					// The prompt fits, but prompt + reserved output does not.
					// Never compact for this: lower the reservation for the
					// rest of the turn, from the backend's exact figures.
					if w, ok := learnedWindowFrom(err); ok {
						s.rememberWindow(w, mainModel)
					}
					if out, ok := budgetRetryOutput(turnOverflow); ok && !budgetRetried {
						budgetRetried = true
						s.setTurnOutputCap(out)
						xlog.Warn("prompt plus output reservation above the window; retrying with a smaller reservation", "max_tokens", out)
						announce("Context window nearly full — reserving less output and retrying…")
						announce(retryResumeStatus)
						continue
					}
					// The prompt leaves less than minOutputTokens of room, or
					// a smaller reservation did not fit either. Only a
					// smaller prompt helps now, so this is a context
					// overflow.
					turnOverflow.Kind = KindContext
				}
			}
			if turnCtx.Err() == nil && turnOverflow.Kind == KindContext {
				if w, ok := learnedWindowFrom(err); ok {
					s.rememberWindow(w, mainModel)
				}
				s.overflowMu.Lock()
				first := s.overflowRetried == 0
				s.overflowMu.Unlock()

				if first {
					// The escalation chain: iterativeTrim (LLM compaction,
					// then a smaller tail, a forced prune and a hard
					// truncation), then basicCompact when none of that
					// fits. The whole chain is ONE recovery attempt.
					cb := s.fragmentTokens()
					status := ""
					if terr := overflowTrim(s, turnCtx); terr == nil {
						status = "Context window exceeded — compacting and retrying…"
					} else if turnCtx.Err() == nil {
						// Report the ORIGINAL overflow, not the trim's
						// failure: the first is the one the user can act on.
						xlog.Warn("overflow recovery: trimming failed, trying basic compaction", "error", terr)
						if berr := s.basicCompact(turnCtx); berr == nil {
							status = "Context window exceeded — using basic fallback compaction and retrying…"
						} else {
							xlog.Warn("overflow recovery: basic compaction failed", "error", berr)
						}
					}
					if status != "" {
						ca := s.fragmentTokens()
						s.overflowMu.Lock()
						s.overflowRetried++
						s.overflowMu.Unlock()
						// Compaction rebuilt the fragment as [summary] + tail,
						// and renderMessages skips system content, so the
						// system prompt may have been dropped without even
						// being represented in the summary. SendMessage's own
						// guard sits ABOVE this loop, so the `continue` below
						// would re-send the turn with no identity, no working
						// directory, no skills index and none of the tool
						// guidance. Re-apply the same guard here: it is a
						// no-op whenever the prompt survived in the kept tail.
						s.ensureSystemPrompt()
						// Announced here, AFTER the chain, and only on the
						// branch that reaches the `continue` below. The status
						// promises a retry; a chain that changed nothing makes
						// none, and the user would be told nib was retrying
						// and then handed the bare overflow error.
						announce(status)
						if s.callbacks.OnCompactDone != nil {
							s.callbacks.OnCompactDone(cb, ca)
						}
						// The compaction notice now records what happened. Put
						// the status back, or a retry that streams a plain
						// answer leaves "compacting and retrying" on screen
						// for the rest of the turn (see retryResumeStatus).
						announce(retryResumeStatus)
						continue
					}
				}
			}

			// Backend failure recovery: a rate limit or a transient error
			// (5xx, dropped connection, timeout) must not end a turn that may
			// have run for many minutes. Wait, then continue from the
			// fragment cogito returned, so the tool calls this attempt
			// finished are kept rather than run again. See retry.go.
			//
			// Bounded by turnRetryBudget attempts in a row without progress,
			// and never after an interrupt: Ctrl+C cancels turnCtx, which
			// also ends the wait.
			if canRetryTurn(turnCtx, err) {
				resume, progressed := resumableFragment(runFragment, newFragment)
				if progressed {
					stalled = 0
				}
				if stalled < turnRetryBudget {
					wait := turnWait(err, stalled)
					announce := func(status string) {
						if s.callbacks.OnStatus != nil {
							s.callbacks.OnStatus(status)
						}
					}
					retry := stalled
					stalled++
					xlog.Warn("backend error, retrying turn", "error", err, "wait", wait, "attempt", stalled)
					if waitTurnRetry(turnCtx, err, wait, retry, announce) == nil {
						s.turnRetryMu.Lock()
						s.turnRetryTotal++
						s.turnRetryMu.Unlock()
						s.commitRun(midTurn, resume)
						continue
					}
				}
			}

			// Reached only when no retry is happening: either this turn never
			// qualified for one, or it already spent its single recovery and
			// overflowed again. In that second case the message must not advise
			// clearing the conversation, because compaction just did the
			// equivalent and the retry has already been made — see
			// contextOverflowRetriedMessage.
			overflow := turnOverflow.Kind == KindContext
			err = humanizeTurnError(err, s.overflowRetries() > 0)
			if s.callbacks.OnError != nil {
				s.callbacks.OnError(err)
			}
			// An overflow that survived recovery rolls the turn back: the
			// message may be what does not fit, and keeping it would make
			// every later turn overflow too. Token usage is kept — the
			// backend billed those tokens regardless.
			if overflow {
				recovered := s.overflowRetries() > 0
				s.historyMu.Lock()
				// When recovery compacted, rolling back to preTurnFragment
				// would undo that compaction: the next turn would start from
				// the same history that overflowed, and overflow again. Keep
				// the compaction and cut out only this turn's own messages.
				kept, ok := []openai.ChatCompletionMessage(nil), false
				if recovered {
					kept, ok = dropFailedTurn(s.fragment.Messages, turnUser, noticesText(notices))
				}
				if ok {
					s.fragment.Messages = kept
					s.messages = dropFailedDisplay(s.messages, text)
				} else {
					s.fragment = preTurnFragment
					s.messages = preTurnMessages
				}
				s.historyMu.Unlock()
				// The rollback dropped the notices too; keep them for the next turn.
				s.restorePendingNotices(notices)
				return "", err
			}
			// An interrupt or any other error keeps the turn. The transcript
			// still shows the user's message and nothing re-sends it, so a
			// rollback left the model answering "go" or "try again" with no
			// idea what it referred to. Keep what the model already has and
			// tell it the turn did not finish.
			note := interruptedTurnNote
			if turnCtx.Err() == nil {
				note = failedTurnNote(err)
			}
			s.commitRun(midTurn, keepFailedTurn(s.fragment, newFragment, note))
			return "", err
		}

		// ExecuteTools returned an answer, so a request completed against the
		// model and the server has prefilled this session's prompt prefix. This
		// is the true boundary: everything above can bail without a request
		// (hooks, reload, fragment assembly, option building), and the paths in
		// SendWithAttachments that return before SendMessage never get here.
		//
		// An interrupted turn deliberately does NOT count. Cancellation after the
		// request reached the server may or may not have populated the cache, and
		// the two mistakes are not symmetric: staying cold costs at worst one
		// extra "preparing the model" label, while claiming warm too early costs
		// the user a silent minute with no explanation.
		if turnCtx.Err() == nil {
			s.prefixWarm.Store(true)
		}

		response = newFragment.LastMessage().Content

		// Each ExecuteTools run reports its own cumulative usage, so the whole
		// tool loop is counted rather than just the final call. The goal
		// stop-gate can run this loop more than once per SendMessage, which is
		// why the turn is counted after the loop and the tokens here.
		//
		// Status is a pointer and a fragment that never reached a backend leaves
		// it nil, so this guards rather than assuming a run always populated it.
		if newFragment.Status != nil {
			s.addUsage(newFragment.Status.CumulativeUsage)
		}

		s.commitRun(midTurn, newFragment, openai.ChatCompletionMessage{
			Role:    "assistant",
			Content: response,
		})

		s.runMu.Lock()
		done := s.goalDone
		goal := s.activeGoalLocked()
		s.runMu.Unlock()

		// Stop when there is no goal, the model declared it done, or the turn
		// was interrupted. Interrupt also pauses the goal (user stopped it).
		if goal == "" || done {
			break
		}
		if turnCtx.Err() != nil {
			s.PauseGoal()
			break
		}

		// Goal still active and unconfirmed: re-feed it and run again.
		reminder := goalReminder(goal)
		s.historyMu.Lock()
		s.fragment = s.fragment.AddMessage("user", reminder)
		s.messages = append(s.messages, openai.ChatCompletionMessage{
			Role:    "user",
			Content: reminder,
		})
		s.historyMu.Unlock()
		if s.callbacks.OnStatus != nil {
			s.callbacks.OnStatus("Goal not yet met — continuing…")
		}
	}

	if s.callbacks.OnResponse != nil {
		s.callbacks.OnResponse(response)
	}

	if s.hooks != nil {
		s.hooks.Fire(s.ctx, hooks.EventStop, "", map[string]any{"event": "Stop"})
	}

	// Auto-compaction: if the last request crossed the configured fraction of
	// the context window, summarize older turns. Never fail the user's turn.
	promptTokens := 0
	if s.fragment.Status != nil {
		promptTokens = s.fragment.Status.LastUsage.PromptTokens
	}
	if s.shouldCompactNow(promptTokens) {
		if s.callbacks.OnStatus != nil {
			s.callbacks.OnStatus("Compacting conversation…")
		}
		cb, ca, cerr := s.compactHistory(turnCtx)
		if cerr != nil {
			xlog.Warn("auto-compaction failed", "error", cerr)
		} else if cb != ca {
			// Compaction dropped the head of the conversation, which
			// may have included the model's read of AGENTS.md and other
			// project instruction files. The system prompt still
			// mentions them, but the model may start tool-calling
			// without re-reading. Flag for a one-time soft nudge at
			// the start of the next turn.
			s.historyMu.Lock()
			s.contextNudgePending = true
			s.historyMu.Unlock()
			if s.callbacks.OnCompactDone != nil {
				s.callbacks.OnCompactDone(cb, ca)
			}
		}
	}

	// One turn per completed SendMessage, counted after the goal loop rather
	// than inside it: a goal that took five ExecuteTools runs is still one
	// exchange the user asked for.
	//
	// Tokens are counted on every run — including the interrupted and failed
	// ones, which return above and add their spend there — while Turns counts
	// only exchanges that completed. So an interrupted session can hold tokens
	// with a lower turn count, and that asymmetry is deliberate: the backend
	// was paid either way, but the user only got the answers it finished.
	s.countTurn()

	return response, nil
}

// Warm issues the same request the next SendMessage would build — same system
// prompt, same tool schemas, same model — capped at one output token, so the
// server prefills and caches the prompt prefix.
//
// It does not touch s.fragment or s.messages and fires no callbacks: nothing
// enters the transcript and the UI never sees it. It honors ctx, so a user who
// sends a message mid-prime cancels it rather than queueing behind it.
//
// The prefix must match the real request to hit the cache, which is why this
// builds through toolOptions rather than assembling its own list. Note that a
// nil error only means the request was accepted — the server may or may not
// have retained the prefix.
func (s *Session) Warm(ctx context.Context) error {
	// Take runMu only to read the goal, and release it before the network call.
	// Holding it across a prime would block Interrupt and the injection path for
	// the whole prefill. Do not "fix" a prime/turn race by widening this without
	// measuring what it blocks.
	goal := s.activeGoal()

	s.historyMu.Lock()
	sys := s.systemPrompt
	s.historyMu.Unlock()

	f := cogito.NewEmptyFragment()
	if sys != "" {
		f = f.AddMessage("system", sys)
	}
	// A minimal user turn: the prime and the real first message diverge only at
	// this content, so the server reuses the whole system-plus-tools prefix.
	f = f.AddMessage("user", "hi")

	// One snapshot for the prime, for the same reason SendMessage takes one:
	// the prefix is only worth caching for the model that will actually be
	// asked for it.
	llm, mainModel := s.currentLLM()

	opts := append([]cogito.Option{cogito.WithContext(ctx)}, s.toolOptions(ctx, goal, mainModel)...)
	// cogito.Prefill rejects force reasoning outright: with it on, the real
	// turn's first request is a reasoning call this prime does not reproduce.
	// Pass the flag through so that refusal surfaces here rather than letting
	// Warm cache a prefix nothing will ask for and report success.
	if s.cogitoOptions.ForceReasoning {
		opts = append(opts, cogito.WithForceReasoning())
	}

	if err := cogito.Prefill(ctx, llm, f, opts...); err != nil {
		return err
	}
	// Priming the prefix is Warm's entire purpose, so a successful prime is by
	// definition the prefix having been sent to the model. A failed or cancelled
	// prime leaves the session cold, as it should.
	s.prefixWarm.Store(true)
	return nil
}

// GetMessages returns all messages in the conversation
func (s *Session) GetMessages() []Message {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
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
//
// The order here is load-bearing, not cosmetic: it decides the order of the
// tool definitions in the request, which the chat template renders into the
// prompt ahead of the user's turn. Ranging cfgClients (a map) directly made
// that order random per turn, so roughly every other request moved the token
// prefix and lost the server's KV cache for the whole system+tools block — a
// full reprocess, seconds of it on CPU. Config servers are therefore emitted
// by sorted name, which is stable across turns *and* across restarts.
func (s *Session) allClients() []*mcp.ClientSession {
	out := append([]*mcp.ClientSession{}, s.clients...)
	if s.skillsClient != nil {
		out = append(out, s.skillsClient)
	}
	for _, name := range slices.Sorted(maps.Keys(s.cfgClients)) {
		out = append(out, s.cfgClients[name])
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
			s.invalidateSchemaCosts()
		}
	}
	for name, srv := range desired {
		if _, ok := s.cfgClients[name]; ok {
			continue
		}
		s.invalidateSchemaCosts()
		transport := wizmcp.TransportForServer(srv)
		// Deliberately pass s.ctx (the session's long-lived context) directly,
		// with no per-connect timeout. The go-sdk's mcp.Client.Connect stores the
		// context it is given for the connection's ENTIRE lifetime (not just the
		// initial handshake): it derives a cancellable context from it and uses
		// that to drive the background "hanging GET" SSE listener and reconnect
		// machinery. A timeout- (or otherwise short-lived) context here would tear
		// down that background listener shortly after connecting, so a later tool
		// call that needs to re-establish its SSE stream fails with the real,
		// user-reported error chain: `hanging GET: failed to reconnect ...
		// context canceled` on a server that is otherwise connected and healthy.
		// This mirrors the built-in host-tool connect path in NewSession, which
		// also hands Connect the raw session context.
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
	s.invalidateSchemaCosts()
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
		s.systemPrompt = cfg.GetPrompt() + s.loadedSkills + s.agentModelGuidance()
		// Inject the harness/version identity so the model knows what it is.
		// Appended after GetPrompt() (which already carries the self-knowledge
		// suffix) so it lands at the end of the system prompt.
		s.systemPrompt += s.harnessIdentity(cfg)
	}
	// Guarded because SetModel writes s.compaction.MaxContextTokens under the
	// same lock when it re-detects the window for a new model. Reload runs at
	// turn start on the turn goroutine, but SetModel runs on whichever
	// goroutine drives the UI, so the two can overlap.
	s.modelMu.Lock()
	s.compaction = cfg.Compaction
	s.modelMu.Unlock()
	// Guarded, unlike its neighbours: the manipulator reads the policy from
	// inside cogito's loop, so a reload that lands while any part of a turn is
	// still winding down would otherwise be a data race.
	s.prunedMu.Lock()
	s.pruning = cfg.ToolOutputPruning
	s.prunedMu.Unlock()
	// Tool-output limits: same reasoning as pruning — the MCP tool handlers
	// read this from inside cogito's loop, so a reload that lands while a
	// turn is winding down would otherwise be a data race.
	s.outputLimitsMu.Lock()
	s.outputLimits = cfg.ToolOutputLimits
	s.outputLimitsMu.Unlock()
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
	if s.lspManager != nil {
		_ = s.lspManager.Close()
	}
	// The durable half of the exit summary. In tmux-widget mode the pane
	// vanishes before anyone can read the printed line, and a benchmark harness
	// wants to parse this rather than scrape a terminal.
	//
	// A warning, not a returned error, and deliberately unlike the hard failure
	// in NewSession: the session is already over, so there is nothing left to
	// refuse — failing here would only turn a lost report into a lost shutdown.
	//
	// Usage() snapshots under its own lock, so the numbers are always
	// self-consistent, but a Close that races a still-running turn (a second
	// Ctrl+C in the TUI goes straight here) will miss whatever that turn folds
	// in afterwards: the file is a floor on the spend and never an
	// overstatement, matching SessionUsage's documented contract.
	if s.traceDir != "" {
		if err := trace.WriteUsage(s.traceDir, s.Usage()); err != nil {
			xlog.Warn("trace: failed to write usage.json", "dir", s.traceDir, "error", err)
		}
	}
	// s.tracer is nil when tracing is disabled; Close is nil-safe.
	if err := s.tracer.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// ensureSystemPrompt adds the system prompt to the persistent fragment only if
// it is not already there. s.fragment persists across turns, so re-adding it
// every turn would accumulate N identical system messages; cogito merges those
// into a position-0 block that grows each turn and defeats the server's
// prompt-prefix KV cache (full re-prefill every message). Add it once.
//
// It is a method rather than four inline lines because there are now TWO places
// that must hold this invariant, and only one of them is obvious. SendMessage
// calls it once per turn, above the goal loop. compactHistory REPLACES the
// fragment with [summary] + tail — and renderMessages skips system content, so
// the prompt is neither kept nor summarised — which means the overflow
// recovery's `continue` re-enters the loop with the identity, working
// directory, skills index and tool guidance all gone, silently, on the one path
// whose whole purpose is to complete the turn. Any future caller that rebuilds
// the fragment mid-turn has the same duty; giving the rule a name is what makes
// it possible to discharge.
//
// Appending is enough even though the prompt belongs at position 0: cogito's
// normalizeSystemMessages hoists every system message into a single deduped
// block at the front before the request goes out.
func (s *Session) ensureSystemPrompt() {
	if s.systemPrompt == "" {
		return
	}
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	if !fragmentHasSystemContent(s.fragment, s.systemPrompt) {
		s.fragment = s.fragment.AddMessage("system", s.systemPrompt)
	}
}

// maybeInjectContextNudge injects a one-time user-role nudge to re-read
// project instruction files (AGENTS.md, CLAUDE.md, etc.) when the
// conversation was compacted at the end of the previous turn and the model
// has not yet re-read any of those files in the current turn. The nudge is
// soft: it adds a user-role message to the fragment but does not block or
// redirect the tool call that triggered it. If no context files are detected
// in the working directory, the nudge is skipped.
func (s *Session) maybeInjectContextNudge() {
	s.historyMu.Lock()
	pending := s.contextNudgePending
	s.historyMu.Unlock()
	if !pending {
		return
	}
	files := types.DetectContextFiles(s.workingDir)
	if len(files) == 0 {
		s.historyMu.Lock()
		s.contextNudgePending = false
		s.historyMu.Unlock()
		return
	}
	s.historyMu.Lock()
	s.contextNudgePending = false
	s.historyMu.Unlock()
	listed := strings.Join(files, ", ")
	s.fragment = s.fragment.AddMessage("user",
		"Reminder: the conversation was just compacted. "+
			"You may have lost track of project instructions. "+
			"Re-read "+listed+" before continuing with tool calls, "+
			"then proceed.")
}

// trackContextFileRead clears the post-compaction nudge flag when the model
// calls the read tool on a path whose base name matches a detected project
// instruction file (AGENTS.md, CLAUDE.md, NIB.md, GEMINI.md). This means:
// if the model re-reads the context file on its own, no nudge fires.
func (s *Session) trackContextFileRead(toolName, argsJSON string) {
	if toolName != "read" {
		return
	}
	s.historyMu.Lock()
	pending := s.contextNudgePending
	s.historyMu.Unlock()
	if !pending {
		return
	}
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil || args.Path == "" {
		return
	}
	base := filepath.Base(args.Path)
	for _, name := range types.ContextFileNames() {
		if base == name {
			s.historyMu.Lock()
			s.contextNudgePending = false
			s.historyMu.Unlock()
			return
		}
	}
}

// fragmentHasSystemContent reports whether the fragment already carries a system
// message with exactly this content, so SendMessage can add the system prompt
// once instead of duplicating it every turn (which would defeat the model
// server's prompt-prefix cache).
func fragmentHasSystemContent(f cogito.Fragment, content string) bool {
	for _, m := range f.Messages {
		if m.Role == "system" && m.Content == content {
			return true
		}
	}
	return false
}

// Model returns the model the session is currently using. Safe to call from
// another goroutine while a turn is running.
func (s *Session) Model() string {
	s.modelMu.RLock()
	defer s.modelMu.RUnlock()
	return s.llmModel
}

// ToolCount returns a best-effort count of registered tools: built-in tools
// (gated by the BuiltinTools allowlist) plus MCP-sourced tools discovered so
// far. MCP tools are discovered lazily during the first turn, so the count
// starts at the built-in baseline and grows once tools are enumerated.
func (s *Session) ToolCount() int {
	n := 0
	// Built-in tools the Session registers directly. spawn_agent via
	// EnableAgentSpawning adds three tools (spawn_agent, check_agent,
	// get_agent_result), so count two extras when it's enabled.
	builtins := []string{
		"spawn_agent",
		"ask_user", "agent_logs",
		"schedule_wakeup",
		"cron", "cron_list", "cron_delete", "cron_pause", "cron_resume", "cron_trigger",
		"read_image", "transcribe_audio", "read_video",
		"memory", "index", "repo_map", "tree", "lsp", "todo_write",
	}
	for _, name := range builtins {
		if s.toolEnabled(name) && !(name == "ask_user" && s.AutoApprove()) {
			n++
			if name == "spawn_agent" {
				n += 2 // check_agent + get_agent_result
			}
		}
	}
	// Self-config tools (list_plugins, install_plugin, etc.).
	for _, d := range selfConfigToolDefs(s.configurator, s.requestReload) {
		if s.toolEnabled(d.name) {
			n++
		}
	}
	// MCP-sourced tools discovered so far (populated lazily during turns).
	s.provenanceMu.Lock()
	n += len(s.externalToolNames)
	s.provenanceMu.Unlock()
	return n
}

// currentLLM returns the client and the model name it was built for as one
// snapshot, so a caller can never pair a client with the wrong model name.
// Callers that need both for a whole turn must take the snapshot once, up
// front: SetModel can land at any point inside a turn.
func (s *Session) currentLLM() (cogito.LLM, string) {
	s.modelMu.RLock()
	defer s.modelMu.RUnlock()
	return s.llm, s.llmModel
}

// SetModel switches the session to a different model, rebuilding the LLM client
// the same way NewSession does: same endpoint and credentials, same
// per-request metadata and reasoning effort, same trace wrapper when tracing is
// on. Relabelling the existing client would not switch anything, and rebuilding
// without re-applying that configuration would silently drop it.
//
// Conversation history is kept: nib is built around persistent context, and
// /compact exists when history needs trimming. Sub-agents follow automatically,
// because newAgentLLM resolves against the session model. The prefix-warm flag
// does not: the new model has never seen this session's prefix.
//
// A turn already in flight finishes on the client it started with (see
// currentLLM); the switch applies from the next turn. Safe to call from another
// goroutine while a turn is running.
//
// The pick applies to this session only and is not written to provider.json.
// Every nib process reads that file at startup, so a pick saved there would
// switch every session started afterwards, including ones running in other
// terminals. An endpoint pick (SwitchProvider) is still saved.
func (s *Session) SetModel(name string) error {
	provider := s.resolvedSessionProvider()
	provider.Model = name
	if err := s.applyProvider(provider, ""); err != nil {
		xlog.Error("could not switch model", "model", name, "error", err)
		return err
	}
	return nil
}

// ResetModel puts the session back on the model the current endpoint itself
// names, and reports that model. It also drops a model saved for the
// endpoint in provider.json (an endpoint pick made with a model, or a /model
// pick saved by an older nib), so the next session starts on the endpoint's
// own model too: nib never edits config.yaml, so the yaml's own model is
// restored by forgetting the pick rather than by rewriting config.yaml.
func (s *Session) ResetModel() (string, error) {
	id := s.EndpointID()
	p, err := s.endpoints.Config(id)
	if err != nil {
		return "", err
	}
	if p.Model == "" {
		return "", fmt.Errorf("%s names no model of its own: pick one with /model", id)
	}
	if err := s.applyProvider(p, id); err != nil {
		return "", err
	}
	if err := endpoint.WriteSaved(s.savedPath, endpoint.Saved{ID: id}); err != nil {
		xlog.Warn("could not clear the saved model", "endpoint", id, "error", err)
	}
	return p.Model, nil
}

// applyProvider rebuilds the session LLM for provider (see SetModel). A
// non-empty id also records which picker entry is now current.
func (s *Session) applyProvider(provider types.ModelProviderConfig, id string) error {
	// Built outside the lock: every input is construction-time state, so
	// nothing here needs to be ordered against a reader.
	name := provider.Model
	llm, err := llmprovider.NewWithStore(provider, s.credStore)
	if err != nil {
		return err
	}
	// Read before tracing wraps the client and hides its SetMaxTokens.
	outCap := factoryOutputCap(llm, provider, s.credStore)
	if s.tracer != nil {
		llm = trace.NewRecordingLLM(llm, s.tracer, name, "")
	}

	s.modelMu.Lock()
	s.llm = llm
	s.llmModel = name
	s.mainProvider = provider
	s.outputCap = outCap
	if id != "" {
		s.endpointID = id
	}
	s.modelMu.Unlock()

	// The new model has never been asked for this session's prefix, so it is
	// genuinely cold. Unlike a server restart or a KV eviction, which the
	// session cannot see, a switch is something we know about, and PrefixWarm
	// prefers a redundant "preparing" label over a silent minute.
	s.prefixWarm.Store(false)

	// The new model's context window and output cap are asked for at the start
	// of the next turn (ensureModelLimits), not here: switching model must not
	// block on the network, and a switch made offline still has to work.
	// Still, clear limitsFor so ensureModelLimits re-runs for the new model,
	// and give MaxContextTokens a best-effort early refresh (when the probe
	// cannot resolve the new model and the static table does not know it,
	// fall back to defaultContextTokens — mirroring NewSession — so the gauge
	// refreshes on switch rather than showing the previous model's window).
	s.modelMu.Lock()
	s.limitsFor = ""
	s.modelMu.Unlock()
	if s.compactionAutoDetected {
		// The probe needs the endpoint the client really talks to, which for a
		// /login provider is its default URL and stored key, not the config's.
		baseURL, apiKey, _ := llmprovider.ModelsEndpoint(provider, s.credStore)
		probeCtx, cancel := context.WithTimeout(s.ctx, probeTimeout)
		v := detectContextSize(probeCtx, baseURL, apiKey, name)
		cancel()
		if v <= 0 {
			v = defaultContextTokens
		}
		s.modelMu.Lock()
		s.compaction.MaxContextTokens = v
		s.modelMu.Unlock()
	}
	return nil
}

// ListModels returns the model IDs the configured endpoint advertises, so a UI
// can offer them for /model. A failing endpoint surfaces as an error rather
// than an empty list.
func (s *Session) ListModels(ctx context.Context) ([]string, error) {
	return s.listModels(ctx, s.resolvedSessionProvider())
}

func (s *Session) listModels(ctx context.Context, provider types.ModelProviderConfig) ([]string, error) {
	ids, _, err := s.modelChoices(ctx, provider)
	return ids, err
}

// modelChoices lists provider's models and whether the list is partial (a
// suggestion that other names may be valid beside; see
// llmprovider.ListModelChoices).
func (s *Session) modelChoices(ctx context.Context, provider types.ModelProviderConfig) ([]string, bool, error) {
	if llmprovider.IsCodex(provider) {
		if model := s.Model(); model != "" {
			return []string{model}, false, nil
		}
		return nil, false, fmt.Errorf("Codex app-server model is not configured")
	}
	return llmprovider.ListModelChoices(ctx, provider, s.credStore)
}

// fetchEndpointModels populates s.endpointModels with the model IDs the
// endpoint advertises. Called lazily by allowedAgentModels on the first
// spawn_agent (not during NewSession, so session init makes no HTTP call).
// Best-effort: on failure (endpoint unreachable, Codex without a configured
// model, timeout) endpointModels stays nil and sub-agent model resolution
// falls back to config-configured models only.
func (s *Session) fetchEndpointModels(ctx context.Context) {
	listCtx, cancel := context.WithTimeout(ctx, ModelListTimeout)
	defer cancel()
	models, err := s.ListModels(listCtx)
	if err != nil {
		return
	}
	s.endpointModels = models
}

// agentModelGuidance returns a system-prompt suffix that advertises the
// endpoint-served models available for the spawn_agent `model` argument, so the
// LLM picks real names instead of inventing ones. Returns "" when there is
// nothing to say: spawn_agent disabled, no endpoint models, or the only model
// served is the main model (nothing to choose from).
//
// The listing is capped so an endpoint that serves hundreds of models (e.g.
// OpenRouter) does not bloat the system prompt. A user who needs a model beyond
// the cap can configure it as an agent type.
func (s *Session) agentModelGuidance() string {
	if !s.toolEnabled("spawn_agent") || len(s.endpointModels) == 0 {
		return ""
	}
	main := s.Model()
	var others []string
	for _, m := range s.endpointModels {
		if m != main {
			others = append(others, m)
		}
	}
	if len(others) == 0 {
		return ""
	}
	const cap = 30
	shown := others
	truncated := false
	if len(shown) > cap {
		shown = shown[:cap]
		truncated = true
	}
	b := "\n\nModels available for the spawn_agent `model` argument (omit it to use the current model " + main + "): " + strings.Join(shown, ", ")
	if truncated {
		b += fmt.Sprintf(", and %d more (use /models to see the full list)", len(others)-cap)
	}
	b += "."
	return b
}

// harnessIdentity returns the version/harness identity string appended to the
// system prompt. It tells the model exactly what version of nib it is running
// as, so it can report this when asked and avoid fabricating version numbers.
// The version and commit are injected at build time via ldflags (see
// .goreleaser.yaml); a locally-built binary reports "dev (local build)".
func (s *Session) harnessIdentity(cfg types.Config) string {
	prog := types.ProgramNameOr(cfg.ProgramName)
	v := internal.PrintableVersion()
	v = strings.TrimSpace(v)
	if v == "()" || v == "" {
		v = "dev (local build)"
	}
	return fmt.Sprintf("\n\nYou are running as %s %s. When the user asks about your version, report this exactly. Do not fabricate version numbers.", prog, v)
}

// ModelListTimeout bounds the endpoint lookup behind /model and /models. Both
// front ends run that lookup on the goroutine that draws the prompt, so an
// endpoint that accepts the connection and then never answers would otherwise
// freeze the UI with no way out.
//
// It is deliberately short. In the TUI this is the only synchronous network
// call on the Update goroutine, so the whole interface, Ctrl+C included, stops
// responding for as long as it runs: the bound is a responsiveness budget, not
// a patience budget. A listing that a local endpoint cannot produce in three
// seconds is a listing the user is better off not waiting for, and the
// degraded outcome is mild (a switch that goes through unverified, never a
// refusal).
const ModelListTimeout = 3 * time.Second

// FormatModelList renders a model listing, marking the current model. Order is
// the endpoint's own; the caller decides whether to sort.
func FormatModelList(models []string, current string) string {
	if len(models) == 0 {
		return "no models available\n"
	}
	var b strings.Builder
	for _, m := range models {
		if m == current {
			b.WriteString("* ")
		} else {
			b.WriteString("  ")
		}
		b.WriteString(m)
		b.WriteByte('\n')
	}
	return b.String()
}

// FormatProviderModelList is FormatModelList headed by the provider that
// serves the list ("Regolo models:"). /models lists the current provider's
// models only; naming it keeps a /login selection and config.yaml's endpoint
// from being mistaken for one another, and hints that another provider's
// models are one /login away.
func FormatProviderModelList(providerName string, models []string, current string) string {
	return providerName + " models:\n" + FormatModelList(models, current)
}

// UnservedModelError is what SwitchModel returns when the endpoint's listing
// does not contain the requested name.
//
// It keeps the headline and the listing separate instead of pre-joining them
// into one message, because the two halves want different rendering: the TUI
// word-wraps error text, which strips the listing's indent column and
// truncates long model IDs, while a fenced block survives verbatim. A front
// end with no such distinction can just use Error().
type UnservedModelError struct {
	Name    string   // the name the user asked for
	Models  []string // what the endpoint does serve
	Current string   // the session's model, marked in the listing
}

// Headline is the one-line explanation, safe to wrap.
func (e *UnservedModelError) Headline() string {
	return fmt.Sprintf("model %q is not served by this endpoint. Available:", e.Name)
}

// Listing is the column-aligned model list, which must NOT be re-wrapped.
func (e *UnservedModelError) Listing() string {
	return FormatModelList(e.Models, e.Current)
}

func (e *UnservedModelError) Error() string {
	return e.Headline() + "\n" + strings.TrimRight(e.Listing(), "\n")
}

// SwitchModel is the checked entry point behind /model <name>: it validates the
// name against what the endpoint advertises and only then calls SetModel. It
// returns the notice to show the user, or an error to show instead. Both front
// ends go through it, so the policy and its wording cannot drift between them.
//
// SetModel's own error only covers rebuilding the LLM client (a bad
// provider config), not an unserved model name — without validating here, a
// typo would switch happily and surface a turn later as a 404 from the
// backend, with nothing pointing at the cause. Validating here turns that
// into an immediate message that names the models the endpoint does serve.
//
// A lookup that fails does NOT veto the switch. The list is a convenience, and
// a user asking for a different model may well be asking precisely because
// something is wrong with the endpoint right now; refusing would leave them
// stuck. The same goes for an endpoint that answers with an empty list. Both
// cases switch (unless SetModel itself errors) and say the name went
// unverified.
func (s *Session) SwitchModel(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("usage: /model <name>")
	}

	lookupCtx, cancel := context.WithTimeout(ctx, ModelListTimeout)
	models, partial, err := s.modelChoices(lookupCtx, s.resolvedSessionProvider())
	cancel()

	switch {
	case partial && !slices.Contains(models, name):
		if serr := s.SetModel(name); serr != nil {
			return "", serr
		}
		return "model: " + name + " (not in the suggested list; the provider decides)", nil
	case err != nil:
		if serr := s.SetModel(name); serr != nil {
			return "", serr
		}
		return "model: " + name + " (unverified: " + err.Error() + ")", nil
	case len(models) == 0:
		if serr := s.SetModel(name); serr != nil {
			return "", serr
		}
		return "model: " + name + " (unverified: the endpoint advertises no models)", nil
	case !slices.Contains(models, name):
		return "", &UnservedModelError{Name: name, Models: models, Current: s.Model()}
	}

	if serr := s.SetModel(name); serr != nil {
		return "", serr
	}
	return "model: " + name, nil
}

// LoginList returns a human-readable list of loginable providers and whether
// each is currently logged in. Used by the /login slash command (no args) in
// both the CLI REPL and TUI.
func (s *Session) LoginList() string {
	defs := provider.Loginable()
	creds, err := s.credStore.All()
	if err != nil {
		return "error reading credentials: " + err.Error()
	}
	credMap := make(map[string]auth.Credential, len(creds))
	for _, c := range creds {
		credMap[c.ProviderID] = c
	}
	var b strings.Builder
	b.WriteString("Providers with login:\n")
	for _, d := range defs {
		status := "not logged in"
		if c, ok := credMap[d.ID]; ok {
			status = "logged in: " + c.DisplayLabel()
		}
		fmt.Fprintf(&b, "  %-12s  %s  (%s)  [%s]\n", d.ID, d.Name, d.LoginKind, status)
	}
	b.WriteString("\nRun: /login <provider>")
	return b.String()
}

// Logout deletes the stored credential for the given provider and returns a
// status message. Used by the /logout <provider> slash command.
func (s *Session) Logout(providerID string) (string, error) {
	def, ok := provider.Get(providerID)
	if !ok {
		return "", fmt.Errorf("unknown provider %q", providerID)
	}
	_, exists, err := s.credStore.Get(def.ID)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("not logged in to %s", def.ID)
	}
	if err := s.credStore.Delete(def.ID); err != nil {
		return "", err
	}
	notice := fmt.Sprintf("Logged out of %s (%s)", def.Name, def.ID)
	if s.EndpointID() != def.ID {
		return notice, nil
	}
	// The session was talking to this provider: leaving it selected would
	// keep a credential-less endpoint as the startup default, and the next
	// session would silently change model instead.
	if err := s.SwitchProvider(endpoint.DefaultID, ""); err != nil {
		// The return switch itself failed (e.g. config.yaml names no model),
		// so the session is stranded ON def with its credential just deleted.
		// Say so rather than the plain success notice: swallowing this left
		// the user believing they were safely back on config.yaml while
		// /login pointed at a command (/login) that no longer switches
		// endpoints at all.
		return notice + fmt.Sprintf(" · still on %s with no login now (%v) · pick another with /endpoint", def.Name, err), nil
	}
	return notice + " · back on " + endpoint.DefaultName + " · model: " + s.Model(), nil
}

// StartLogin begins a login flow for the given provider. For OAuth-code and
// device-code providers, it returns a LoginFlow whose Complete method must be
// called asynchronously to finish the flow. For Copilot, it imports the token
// synchronously and returns a completed flow. For API-key providers, it
// returns an error directing the user to the CLI.
func (s *Session) StartLogin(ctx context.Context, providerID string) (*auth.LoginFlow, error) {
	def, ok := provider.Get(providerID)
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", providerID)
	}
	switch def.LoginKind {
	case provider.LoginOAuthCode, provider.LoginDeviceCode:
		return auth.StartLogin(ctx, s.credStore, def)

	case provider.LoginCopilot:
		token, err := copilot.ResolveToken()
		if err != nil {
			return nil, fmt.Errorf("import copilot token: %w\nInstall gh CLI and run 'gh auth login', or set %s", err, def.EnvVar)
		}
		cred, err := auth.LoginAPIKey(s.credStore, def, token)
		if err != nil {
			return nil, err
		}
		return auth.NewLoginFlow(
			def.ID,
			"Imported Copilot token for "+def.Name,
			"",
			func(context.Context) (auth.Credential, error) { return cred, nil },
		), nil

	case provider.LoginAPIKey:
		return nil, fmt.Errorf("%s logs in with an API key: use SaveAPIKey", def.ID)

	default:
		return nil, fmt.Errorf("provider %s has no login flow", def.ID)
	}
}
