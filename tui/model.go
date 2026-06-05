package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/types"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/chat"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/slash"
)

// ChatMessage represents a message in the chat history
type ChatMessage struct {
	Role      string
	Content   string
	Name      string // tool name, for Role == "tool"
	Arguments string // marshaled call args, for Role == "tool"
	AgentID   string // issuing sub-agent, for Role == "tool" (empty = root agent)
}

// Model represents the TUI state
type Model struct {
	// UI components
	viewport viewport.Model
	textarea textarea.Model
	spinner  spinner.Model

	// Chat state
	messages     []ChatMessage
	session      *chat.Session
	ctx          context.Context
	cancel       context.CancelFunc
	transports   []mcp.Transport
	shellJobs    *wizmcp.ShellJobs
	cfg          types.Config
	sessionReady bool

	// UI state
	width     int
	height    int
	maxHeight int // Configured max height (0 = no limit)
	loading   bool
	// interruptArmed is set after a first Ctrl+C interrupts an in-flight turn,
	// so a second Ctrl+C exits instead of just re-interrupting. Reset when a new
	// turn starts and when the turn ends.
	interruptArmed bool
	status         string
	reasoning      string
	err            error
	output         string // Command to output to shell on exit
	quitting       bool

	// Tool approval state
	pendingTool      *chat.ToolCallRequest
	awaitingApproval bool
	// approvalEditing distinguishes the two approval sub-modes: false is the
	// default key-driven choice mode (single y/a/n/e/A/Esc keypresses, input
	// hidden); true is edit mode where the textarea is shown for a free-form
	// change.
	approvalEditing bool

	// ask_user state
	pendingAsk      *chat.AskRequest
	awaitingAsk     bool
	askRequestChan  chan chat.AskRequest
	askResponseChan chan string
	wakeupChan      chan chat.WakeupRequest
	compactChan     chan [2]int // {before, after} token counts from auto-compaction

	// contextTokens is the current conversation size shown in the footer badge.
	// Updated after each turn and after compaction; 0 hides the badge.
	contextTokens int

	// Animation state
	statusPhase int

	// Sub-agent jobs state
	jobs           []agentJob
	agentEventChan chan chat.AgentEvent

	// Ctrl+O log viewer state.
	showLogs    bool           // viewer open
	logSel      int            // selected index in the unified jobs list (list mode)
	logOpenID   string         // when non-empty: drilled into this job's full log
	logOpenKind string         // "agent" | "shell" for the open job
	logVP       viewport.Model // scrollable full-log view

	// Auto-notify state: completion notices for backgrounded work that the
	// assistant should react to.
	notifiedJobs   map[string]bool // job/agent ids already notified (or suppressed)
	pendingNotices []string        // queued notices awaiting an idle moment
	bgAgents       map[string]bool // sub-agent ids the user backgrounded (Ctrl+B)

	// Unified `/` completion state
	completion compState

	// Markdown renderers cached per wrap width (glamour renderers are
	// width-bound and expensive to build). At most a couple of distinct
	// widths exist in practice (one per message-prefix width).
	mdRenderers map[int]*glamour.TermRenderer

	// Channels for async communication with callbacks
	statusChan       chan string
	reasoningChan    chan string
	toolRequestChan  chan chat.ToolCallRequest
	toolResponseChan chan chat.ToolCallResponse
	toolResultChan   chan chat.ToolResult
}

// responseMsg is sent when the AI responds
type responseMsg struct {
	content string
	err     error
}

// compactResultMsg is the outcome of a manual /compact run.
type compactResultMsg struct {
	before, after int
	err           error
}

// compactNoticeMsg is an auto-compaction notice pushed from the session goroutine.
type compactNoticeMsg [2]int

// statusMsg is sent for status updates
type statusMsg string

// reasoningMsg is sent for reasoning updates
type reasoningMsg string

// toolCallMsg is sent when a tool call needs approval
type toolCallMsg chat.ToolCallRequest

// askMsg is sent when the agent asks the user a question.
type askMsg chat.AskRequest

// agentEventMsg is sent for sub-agent lifecycle updates.
type agentEventMsg chat.AgentEvent

// toolResultPreviewLines bounds how many lines of a tool result we show inline.
const toolResultPreviewLines = 12

// toolResultMsg carries a finished tool's output to the UI.
type toolResultMsg chat.ToolResult

// sessionReadyMsg is sent when the session is initialized
type sessionReadyMsg struct {
	session *chat.Session
	err     error
}

// NewModel creates a new TUI model
func NewModel(ctx context.Context, cfg types.Config, height int, shellJobs *wizmcp.ShellJobs, transports ...mcp.Transport) Model {
	ctx, cancel := context.WithCancel(ctx)

	ta := textarea.New()
	ta.Placeholder = "ask anything…"
	ta.Focus()
	ta.Prompt = theme.PromptGlyph + " "
	ta.FocusedStyle.Prompt = theme.Prompt
	ta.CharLimit = 4096
	ta.SetWidth(80)
	// Single-line input: Enter sends (newline insertion is disabled), so a
	// taller textarea would just repeat the `›` prompt on every empty row.
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(false) // Enter sends message

	vp := viewport.New(80, 10)
	vp.SetContent("")

	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = theme.Help

	// Calculate max height - negative means percentage, positive means lines
	maxH := height
	if maxH < 0 {
		maxH = 0 // Will be calculated on first WindowSizeMsg
	}

	m := Model{
		viewport:         vp,
		logVP:            viewport.New(80, 10),
		textarea:         ta,
		spinner:          s,
		messages:         []ChatMessage{},
		ctx:              ctx,
		cancel:           cancel,
		maxHeight:        maxH,
		transports:       transports,
		shellJobs:        shellJobs,
		cfg:              cfg,
		height:           height,
		agentEventChan:   make(chan chat.AgentEvent, 16),
		statusChan:       make(chan string, 10),
		reasoningChan:    make(chan string, 10),
		toolRequestChan:  make(chan chat.ToolCallRequest),
		toolResponseChan: make(chan chat.ToolCallResponse),
		toolResultChan:   make(chan chat.ToolResult, 64),
		askRequestChan:   make(chan chat.AskRequest),
		askResponseChan:  make(chan string),
		wakeupChan:       make(chan chat.WakeupRequest, 8),
		compactChan:      make(chan [2]int, 4),
		mdRenderers:      make(map[int]*glamour.TermRenderer),
	}
	m.completion.setRegistries(cfg.Commands, cfg.Skills, cfg.Agents)
	return m
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.spinner.Tick,
		m.initSession(),
	)
}

// initSession creates the chat session
func (m Model) initSession() tea.Cmd {
	return func() tea.Msg {
		callbacks := chat.Callbacks{
			OnStatus: func(status string) {
				select {
				case m.statusChan <- status:
				default:
				}
			},
			OnReasoning: func(reasoning string) {
				select {
				case m.reasoningChan <- reasoning:
				default:
				}
			},
			OnToolCall: func(req chat.ToolCallRequest) chat.ToolCallResponse {
				// Send tool request and wait for user response
				m.toolRequestChan <- req
				return <-m.toolResponseChan
			},
			OnAskUser: func(req chat.AskRequest) string {
				m.askRequestChan <- req
				return <-m.askResponseChan
			},
			OnScheduleWakeup: func(req chat.WakeupRequest) string {
				// Non-blocking: hand the request to the UI loop and confirm now.
				select {
				case m.wakeupChan <- req:
					return fmt.Sprintf("Scheduled a wake-up in %ds: %q. You'll be re-invoked then to check on it.", req.DelaySeconds, req.Note)
				default:
					return "Could not schedule wake-up (too many pending)."
				}
			},
			OnAgentEvent: func(ev chat.AgentEvent) {
				select {
				case m.agentEventChan <- ev:
				default:
				}
			},
			OnCompactDone: func(before, after int) {
				select {
				case m.compactChan <- [2]int{before, after}:
				default:
				}
			},
			OnToolResult: func(res chat.ToolResult) {
				select {
				case m.toolResultChan <- res:
				default:
				}
			},
		}

		session, err := chat.NewSession(m.ctx, m.cfg, callbacks, m.transports...)
		return sessionReadyMsg{session: session, err: err}
	}
}

// Update handles messages and updates the model
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Ctrl+O log viewer: intercepts navigation/scroll keys while open. Ctrl+C
		// falls through so it can still interrupt/quit.
		if m.showLogs && msg.Type != tea.KeyCtrlC {
			jobs := m.unifiedJobs()
			if m.logOpenID == "" {
				// LIST mode
				switch {
				case msg.Type == tea.KeyEsc, msg.Type == tea.KeyCtrlO:
					m.showLogs = false
					return m, nil
				case msg.Type == tea.KeyUp:
					if m.logSel > 0 {
						m.logSel--
					}
					return m, nil
				case msg.Type == tea.KeyDown:
					if m.logSel < len(jobs)-1 {
						m.logSel++
					}
					return m, nil
				case msg.Type == tea.KeyEnter:
					if m.logSel >= 0 && m.logSel < len(jobs) {
						m.logOpenID = jobs[m.logSel].ID
						m.logOpenKind = jobs[m.logSel].Kind
						m.syncLogViewport()
					}
					return m, nil
				case msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && (msg.Runes[0] == 'k' || msg.Runes[0] == 'K'):
					if m.logSel >= 0 && m.logSel < len(jobs) {
						m.killSelected(m.logSel + 1)
					}
					return m, nil
				}
				return m, nil // swallow other keys in list mode
			}
			// LOG mode (drilled into one job): Esc -> back to list; Ctrl+O -> close.
			switch msg.Type {
			case tea.KeyEsc:
				m.logOpenID = ""
				m.logOpenKind = ""
				return m, nil
			case tea.KeyCtrlO:
				m.showLogs = false
				m.logOpenID = ""
				m.logOpenKind = ""
				return m, nil
			}
			// Anything else scrolls the log viewport (up/down/pgup/pgdn/home/end).
			var vpCmd tea.Cmd
			m.logVP, vpCmd = m.logVP.Update(msg)
			return m, vpCmd
		}
		// Tool approval is a distinct key-driven mode: in choice mode the chat
		// input is hidden and y/a/n/e/A/Esc act as single keypresses; edit mode
		// (entered with `e`) shows the textarea for a free-form change.
		if m.awaitingApproval {
			if !m.approvalEditing {
				switch {
				case msg.Type == tea.KeyEsc:
					return m.resolveApproval(chat.ToolCallResponse{Approved: false})
				case msg.Type == tea.KeyRunes && len(msg.Runes) == 1:
					switch msg.Runes[0] {
					case 'y', 'Y':
						return m.resolveApproval(chat.ToolCallResponse{Approved: true})
					case 'a':
						return m.resolveApproval(chat.ToolCallResponse{Approved: true, AlwaysAllow: true})
					case 'A':
						return m.resolveApproval(chat.ToolCallResponse{Approved: true, AllowAllTurn: true})
					case 'n', 'N':
						return m.resolveApproval(chat.ToolCallResponse{Approved: false})
					case 'e', 'E':
						m.approvalEditing = true
						m.textarea.Reset()
						m.updateViewport()
						return m, nil
					}
				}
				// Swallow every other key in choice mode, but let Ctrl+C fall
				// through to the normal interrupt/quit handling below.
				if msg.Type != tea.KeyCtrlC {
					return m, nil
				}
			} else if msg.Type == tea.KeyEsc {
				// Edit mode: Esc cancels back to choice mode without denying.
				m.approvalEditing = false
				m.textarea.Reset()
				m.updateViewport()
				return m, nil
			}
		}
		switch msg.Type {
		case tea.KeyCtrlC:
			// First Ctrl+C on an in-flight turn interrupts the request but keeps
			// the session open; a second Ctrl+C (or Ctrl+C while idle) exits.
			if m.isWorking() && !m.interruptArmed {
				if m.session != nil {
					m.session.Interrupt()
				}
				m.interruptArmed = true
				m.status = "Interrupting… (Ctrl+C again to exit)"
				return m, nil
			}
			return m.quit()

		case tea.KeyEsc:
			return m.quit()

		case tea.KeyCtrlY:
			// Yank nib's last suggested command to the shell and exit, so the
			// Ctrl+Space widget inserts it at the prompt. No-op if there's
			// nothing to yank or a turn is still in flight.
			if !m.sessionReady || m.loading {
				return m, nil
			}
			cmd := lastSuggestedCommand(m.messages)
			if cmd == "" {
				m.status = "no command to use yet"
				return m, nil
			}
			m.output = cmd
			return m.quit()

		case tea.KeyCtrlB:
			// Background the running foreground work: a sub-agent first,
			// otherwise a running foreground shell command.
			if m.sessionReady && m.session != nil {
				if id := m.firstRunningJobID(); id != "" {
					_ = m.session.AgentManager().Detach(id)
					if m.bgAgents == nil {
						m.bgAgents = map[string]bool{}
					}
					m.bgAgents[id] = true // eligible for auto-notify on completion
					return m, nil
				}
			}
			if id, ok := m.shellJobs.DetachForeground(); ok {
				m.status = "Backgrounded shell job " + id
				m.updateViewport()
			}
			return m, nil

		case tea.KeyCtrlO:
			// Toggle the navigable log viewer.
			if !m.sessionReady {
				return m, nil
			}
			m.showLogs = !m.showLogs
			m.logSel = 0
			m.logOpenID = ""
			m.logOpenKind = ""
			return m, nil

		case tea.KeyTab:
			if m.completion.active {
				if ins, ok := m.completion.accept(); ok {
					m.textarea.SetValue(ins)
					m.completion.sync(ins)
				}
				return m, nil
			}

		case tea.KeyUp:
			if m.completion.active {
				m.completion.up()
				return m, nil
			}

		case tea.KeyDown:
			if m.completion.active {
				m.completion.down()
				return m, nil
			}

		case tea.KeyEnter:
			// Accept an open completion instead of submitting.
			if m.completion.active {
				if ins, ok := m.completion.accept(); ok {
					m.textarea.SetValue(ins)
					m.completion.sync(ins)
				}
				return m, nil
			}

			if m.loading || !m.sessionReady {
				return m, nil
			}

			input := strings.TrimSpace(m.textarea.Value())
			if input == "" {
				return m, nil
			}

			// Check if we're answering an ask_user question
			if m.awaitingAsk && m.pendingAsk != nil {
				answer := parseAskAnswer(m.textarea.Value(), *m.pendingAsk)
				m.messages = append(m.messages, ChatMessage{Role: "user", Content: answer})
				m.textarea.Reset()
				m.awaitingAsk = false
				m.pendingAsk = nil
				m.loading = true
				m.status = "Thinking…"
				m.updateViewport()
				m.askResponseChan <- answer
				return m, nil
			}

			// Check if we're in tool approval mode
			if m.awaitingApproval {
				return m.handleToolApproval(input)
			}

			// Echo the user's literal input, then resolve it (command/skill/agent).
			m.messages = append(m.messages, ChatMessage{Role: "user", Content: input})
			m.textarea.Reset()
			m.completion.sync("")

			action := slash.Resolve(input, m.cfg.Commands, m.cfg.Skills, m.cfg.Agents)
			switch action.Kind {
			case slash.KindError:
				m.messages = append(m.messages, ChatMessage{Role: "error", Content: action.Err})
				m.updateViewport()
				return m, nil
			case slash.KindLoadSkill:
				notice, err := m.session.LoadSkill(action.Skill)
				if err != nil {
					m.messages = append(m.messages, ChatMessage{Role: "error", Content: err.Error()})
				} else {
					m.messages = append(m.messages, ChatMessage{Role: "agent", Content: notice})
				}
				m.updateViewport()
				return m, nil
			case slash.KindCompact:
				m.loading = true
				m.interruptArmed = false
				m.status = "Compacting conversation…"
				m.updateViewport()
				return m, m.compactCmd()
			default: // slash.KindSend
				m.loading = true
				m.interruptArmed = false
				m.status = ""
				m.updateViewport()
				return m, m.sendMessage(action.Text)
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updateDimensions()

	case sessionReadyMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.session = msg.session
		m.sessionReady = true
		// Start listening for callbacks
		cmds = append(cmds, m.listenStatus(), m.listenReasoning(), m.listenToolRequest(), m.listenToolResult(), m.listenAskRequest(), m.listenAgentEvents(), m.shellTick(), m.listenWakeup(), m.listenCompact())

	case responseMsg:
		m.loading = false
		m.interruptArmed = false
		m.status = ""
		m.reasoning = ""
		if msg.err != nil {
			if errors.Is(msg.err, context.Canceled) {
				m.messages = append(m.messages, ChatMessage{Role: "agent", Content: "interrupted."})
			} else {
				m.err = msg.err
				m.messages = append(m.messages, ChatMessage{Role: "error", Content: msg.err.Error()})
			}
		} else {
			m.messages = append(m.messages, ChatMessage{Role: "assistant", Content: msg.content})
		}
		if m.session != nil {
			m.contextTokens = m.session.ContextTokens()
		}
		m.updateViewport()
		// A background job may have finished during this turn; react to it now
		// that we're idle again.
		if c := m.autoNotifyCmd(); c != nil {
			cmds = append(cmds, c)
		}

	case compactResultMsg:
		m.loading = false
		m.status = ""
		if msg.err != nil {
			m.messages = append(m.messages, ChatMessage{Role: "error", Content: "compaction failed: " + msg.err.Error()})
		} else if msg.before == msg.after {
			m.messages = append(m.messages, ChatMessage{Role: "agent", Content: "Nothing to compact yet."})
		} else {
			m.messages = append(m.messages, ChatMessage{Role: "agent", Content: compactNotice(msg.before, msg.after)})
			m.contextTokens = msg.after
		}
		m.updateViewport()
		return m, nil

	case compactNoticeMsg:
		m.messages = append(m.messages, ChatMessage{Role: "agent", Content: compactNotice(msg[0], msg[1])})
		m.contextTokens = msg[1]
		m.updateViewport()
		return m, m.listenCompact()

	case shellTickMsg:
		// Periodic refresh so the shell-jobs footer reflects jobs that finish
		// (or are started by the model) while the user is idle, and auto-notify
		// the assistant about finished background work.
		if c := m.autoNotifyCmd(); c != nil {
			cmds = append(cmds, c)
		}
		m.updateViewport()
		if m.showLogs && m.logOpenID != "" {
			m.syncLogViewport()
		}
		if !m.quitting {
			cmds = append(cmds, m.shellTick())
		}

	case wakeupScheduledMsg:
		// Arm a timer for the requested delay, then keep listening for more.
		req := chat.WakeupRequest(msg)
		d := time.Duration(req.DelaySeconds) * time.Second
		note := req.Note
		cmds = append(cmds,
			tea.Tick(d, func(time.Time) tea.Msg { return wakeupFireMsg{note: note} }),
			m.listenWakeup(),
		)

	case wakeupFireMsg:
		// The delay elapsed: queue a wake-up notice and react when idle (reusing
		// the auto-notify turn machinery).
		note := msg.note
		if note == "" {
			note = "(no note)"
		}
		m.pendingNotices = append(m.pendingNotices, "scheduled wake-up: "+note)
		if c := m.autoNotifyCmd(); c != nil {
			cmds = append(cmds, c)
		}

	case statusMsg:
		m.status = string(msg)
		m.updateViewport()
		// Continue listening for more status updates
		cmds = append(cmds, m.listenStatus())

	case reasoningMsg:
		m.reasoning = string(msg)
		m.updateViewport()
		// Continue listening for more reasoning updates
		cmds = append(cmds, m.listenReasoning())

	case toolCallMsg:
		m.pendingTool = (*chat.ToolCallRequest)(&msg)
		m.awaitingApproval = true
		m.approvalEditing = false // every approval starts in key-driven choice mode
		m.loading = false         // Allow user input for approval
		m.textarea.Focus()        // Ensure textarea is focused for input
		m.updateViewport()
		// Continue listening for more tool requests
		cmds = append(cmds, m.listenToolRequest())

	case askMsg:
		req := chat.AskRequest(msg)
		m.pendingAsk = &req
		m.awaitingAsk = true
		m.loading = false
		m.textarea.Focus()
		m.updateViewport()
		cmds = append(cmds, m.listenAskRequest())

	case agentEventMsg:
		// Update value-receiver copy via pointer helper, then write back.
		ev := chat.AgentEvent(msg)
		am := m
		(&am).applyAgentEvent(ev)
		m = am
		// On completion, always show the stats marker line (e.g.
		// "sub-agent explore finished · 3 tools · …"); when the agent produced a
		// final result, also surface it inline as one labeled block. Per-tool
		// activity stays in the Ctrl+O log viewer.
		if line := agentTranscriptLine(ev); line != "" {
			m.messages = append(m.messages, ChatMessage{Role: "agent", Content: line})
		}
		if ev.Status == chat.AgentStatusCompleted && strings.TrimSpace(ev.Result) != "" {
			typ := ev.Type
			if typ == "" {
				typ = "agent"
			}
			m.messages = append(m.messages, ChatMessage{
				Role:    "tool",
				Name:    typ,
				AgentID: ev.ID,
				Content: chat.PreviewResult(ev.Result, toolResultPreviewLines),
			})
		}
		m.updateViewport()
		if m.showLogs && m.logOpenID != "" {
			m.syncLogViewport()
		}
		// Continue listening for more agent events
		cmds = append(cmds, m.listenAgentEvents())

	case toolResultMsg:
		res := chat.ToolResult(msg)
		// Quiet by default: only the ROOT agent's tool results stream inline.
		// Sub-agent tool activity lives in the Ctrl+O log viewer; a sub-agent's
		// final result is shown inline on completion (see agentEventMsg). Preview
		// once at append time so we don't retain a multi-MB raw result.
		if res.AgentID == "" {
			if preview := chat.PreviewResult(res.Result, toolResultPreviewLines); preview != "" {
				m.messages = append(m.messages, ChatMessage{Role: "tool", Name: res.Name, Arguments: res.Arguments, Content: preview})
				m.updateViewport()
			}
		}
		// Continue listening for more tool results
		cmds = append(cmds, m.listenToolResult())

	case spinner.TickMsg:
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
		// Rotate status phase for animated messages
		if m.loading {
			m.statusPhase = (m.statusPhase + 1) % 12
			m.updateViewport()
		}
	}

	// Update textarea (only if not loading)
	// Note: We allow updates during approval states so users can type their response
	if !m.loading {
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
		m.completion.sync(m.textarea.Value())
	}

	// Update viewport
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// sendMessage sends a message to the AI
func (m Model) sendMessage(text string) tea.Cmd {
	return func() tea.Msg {
		response, err := m.session.SendMessage(text)
		return responseMsg{content: response, err: err}
	}
}

// shellTickMsg drives a periodic refresh of the shell-jobs footer.
type shellTickMsg struct{}

// shellTick schedules the next shell-jobs footer refresh.
func (m Model) shellTick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return shellTickMsg{} })
}

// wakeupScheduledMsg is emitted when the agent schedules an in-session wake-up.
type wakeupScheduledMsg chat.WakeupRequest

// wakeupFireMsg is emitted when a scheduled wake-up's delay elapses.
type wakeupFireMsg struct{ note string }

// listenWakeup waits for the agent to schedule a wake-up.
func (m Model) listenWakeup() tea.Cmd {
	return func() tea.Msg {
		select {
		case req := <-m.wakeupChan:
			return wakeupScheduledMsg(req)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// listenCompact waits for an auto-compaction notice from the session.
func (m Model) listenCompact() tea.Cmd {
	return func() tea.Msg {
		select {
		case v := <-m.compactChan:
			return compactNoticeMsg(v)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// compactCmd runs a manual /compact (an LLM call) off the event loop.
func (m Model) compactCmd() tea.Cmd {
	return func() tea.Msg {
		before, after, err := m.session.CompactHistory()
		return compactResultMsg{before: before, after: after, err: err}
	}
}

// listenStatus listens for status updates from the session
func (m Model) listenStatus() tea.Cmd {
	return func() tea.Msg {
		select {
		case status := <-m.statusChan:
			return statusMsg(status)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// listenReasoning listens for reasoning updates from the session
func (m Model) listenReasoning() tea.Cmd {
	return func() tea.Msg {
		select {
		case reasoning := <-m.reasoningChan:
			return reasoningMsg(reasoning)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// listenAgentEvents listens for sub-agent lifecycle events from the session
func (m Model) listenAgentEvents() tea.Cmd {
	return func() tea.Msg {
		select {
		case ev := <-m.agentEventChan:
			return agentEventMsg(ev)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// listenToolRequest listens for tool call requests from the session
func (m Model) listenToolRequest() tea.Cmd {
	return func() tea.Msg {
		select {
		case req := <-m.toolRequestChan:
			return toolCallMsg(req)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// listenToolResult listens for finished tool results from the session.
func (m Model) listenToolResult() tea.Cmd {
	return func() tea.Msg {
		select {
		case res := <-m.toolResultChan:
			return toolResultMsg(res)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// listenAskRequest listens for ask_user requests from the session.
func (m Model) listenAskRequest() tea.Cmd {
	return func() tea.Msg {
		select {
		case req := <-m.askRequestChan:
			return askMsg(req)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// handleToolApproval handles a free-form adjustment typed in edit mode. An empty
// input is treated as a plain approval. (Choice-mode keypresses are handled by
// the interception block in Update and never reach here.)
func (m Model) handleToolApproval(input string) (tea.Model, tea.Cmd) {
	input = strings.TrimSpace(input)
	if input == "" {
		return m.resolveApproval(chat.ToolCallResponse{Approved: true})
	}
	return m.resolveApproval(chat.ToolCallResponse{Approved: true, Adjustment: input})
}

// resolveApproval finalizes a tool-approval decision: tear down approval state,
// resume the spinner, and hand the response back to the waiting callback.
func (m Model) resolveApproval(resp chat.ToolCallResponse) (tea.Model, tea.Cmd) {
	m.awaitingApproval = false
	m.approvalEditing = false
	m.pendingTool = nil
	m.textarea.Reset()
	m.loading = true
	m.status = "Executing tool..."
	m.updateViewport()
	return m, func() tea.Msg {
		m.toolResponseChan <- resp
		return nil
	}
}

// updateDimensions updates component dimensions based on window size
func (m *Model) updateDimensions() {
	// Constrain height to maxHeight if set
	effectiveHeight := m.height
	if m.maxHeight > 0 && effectiveHeight > m.maxHeight {
		effectiveHeight = m.maxHeight
	}

	headerHeight := 2
	footerHeight := 3 // single-line input + help line + spacing
	statusHeight := 1

	vpHeight := effectiveHeight - headerHeight - footerHeight - statusHeight
	if vpHeight < 5 {
		vpHeight = 5
	}

	m.viewport.Width = m.width
	m.viewport.Height = vpHeight
	m.logVP.Width = m.width
	m.logVP.Height = vpHeight
	m.textarea.SetWidth(m.width - 2)
}

// wrapText wraps text to fit within the specified width, preserving existing newlines
func wrapText(text string, width int) string {
	if width <= 0 {
		return text
	}

	var result strings.Builder
	lines := strings.Split(text, "\n")

	for _, line := range lines {
		if line == "" {
			result.WriteString("\n")
			continue
		}

		// Calculate the visual width (accounting for ANSI codes)
		visualWidth := lipgloss.Width(line)
		if visualWidth <= width {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		// Need to wrap this line
		words := strings.Fields(line)
		if len(words) == 0 {
			result.WriteString("\n")
			continue
		}

		currentLine := strings.Builder{}
		currentWidth := 0

		for i, word := range words {
			wordWidth := lipgloss.Width(word)

			// If a single word is longer than width, truncate it on a rune
			// boundary (byte slicing here would split a multibyte rune).
			if wordWidth > width && currentWidth == 0 {
				result.WriteString(truncateRunes(word, width))
				result.WriteString("\n")
				continue
			}

			if currentWidth > 0 {
				// Check if adding this word would exceed width
				if currentWidth+1+wordWidth > width {
					// Write current line and start new one
					result.WriteString(currentLine.String())
					result.WriteString("\n")
					currentLine.Reset()
					currentWidth = 0
				} else {
					// Add space before word
					currentLine.WriteString(" ")
					currentWidth += 1
				}
			}

			currentLine.WriteString(word)
			currentWidth += wordWidth

			// If this is the last word, write the line
			if i == len(words)-1 {
				result.WriteString(currentLine.String())
				result.WriteString("\n")
			}
		}
	}

	return result.String()
}

// truncateRunes shortens word to at most width display columns, breaking on a
// rune boundary and appending an ellipsis when there is room for it.
func truncateRunes(word string, width int) string {
	runes := []rune(word)
	if width <= 0 {
		return ""
	}
	if len(runes) <= width {
		return word
	}
	if width <= 1 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
}

// updateViewport updates the viewport content with chat messages
// markdownFor returns a glamour renderer for the given wrap width, building and
// caching one per distinct width. Returns nil on construction error (callers
// fall back to plain wrapText).
func (m *Model) markdownFor(width int) *glamour.TermRenderer {
	if width < 1 {
		width = 1
	}
	if m.mdRenderers == nil {
		m.mdRenderers = make(map[int]*glamour.TermRenderer)
	}
	if r, ok := m.mdRenderers[width]; ok {
		return r
	}
	r, err := nibMarkdownRenderer(width)
	if err != nil {
		return nil
	}
	m.mdRenderers[width] = r
	return r
}

func (m *Model) updateViewport() {
	var sb strings.Builder

	// Calculate available width for content (use viewport width, not terminal width)
	contentWidth := m.viewport.Width
	if contentWidth <= 0 {
		contentWidth = m.width
	}
	if contentWidth <= 0 {
		contentWidth = 80 // fallback
	}

	for _, msg := range m.messages {
		switch msg.Role {
		case "user":
			prefix := userStyle.Render("you") + " " + theme.SepStyle.Render(theme.Sep) + " "
			prefixWidth := lipgloss.Width(prefix)
			wrappedContent := wrapText(msg.Content, contentWidth-prefixWidth)
			// Add prefix to first line, indent continuation lines
			lines := strings.Split(strings.TrimRight(wrappedContent, "\n"), "\n")
			for i, line := range lines {
				if i == 0 {
					// First line: prefix + content
					sb.WriteString(prefix)
					sb.WriteString(line)
				} else {
					// Continuation lines: indent with spaces only (no prefix)
					sb.WriteString(strings.Repeat(" ", prefixWidth))
					sb.WriteString(line)
				}
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
		case "assistant":
			prefix := assistantStyle.Render(theme.BrandName) + " " + theme.SepStyle.Render(theme.Sep) + " "
			prefixWidth := lipgloss.Width(prefix)
			wrappedContent := renderMarkdownWith(m.markdownFor(contentWidth-prefixWidth), msg.Content, contentWidth-prefixWidth)
			// Add prefix to first line, indent continuation lines
			lines := strings.Split(strings.TrimRight(wrappedContent, "\n"), "\n")
			for i, line := range lines {
				if i == 0 {
					// First line: prefix + content
					sb.WriteString(prefix)
					sb.WriteString(line)
				} else {
					// Continuation lines: indent with spaces only (no prefix)
					sb.WriteString(strings.Repeat(" ", prefixWidth))
					sb.WriteString(line)
				}
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
		case "agent":
			prefix := theme.Subtle.Render(theme.SubAgent) + " "
			prefixWidth := lipgloss.Width(prefix)
			wrappedContent := renderMarkdownWith(m.markdownFor(contentWidth-prefixWidth), msg.Content, contentWidth-prefixWidth)
			lines := strings.Split(strings.TrimRight(wrappedContent, "\n"), "\n")
			for i, line := range lines {
				if i == 0 {
					sb.WriteString(prefix)
					sb.WriteString(agentStyle.Render(line))
				} else {
					sb.WriteString(strings.Repeat(" ", prefixWidth))
					sb.WriteString(agentStyle.Render(line))
				}
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
		case "tool":
			// Calm, dim block: a header naming the tool, then the pretty/truncated
			// output indented and dimmed beneath it.
			label := msg.Name
			if msg.Arguments != "" {
				// First line of the friendly summary makes the clearest header.
				summary := chat.FormatToolCall(msg.Name, msg.Arguments)
				if nl := strings.IndexByte(summary, '\n'); nl >= 0 {
					summary = summary[:nl]
				}
				if summary != "" {
					label = summary
				}
			}
			if msg.AgentID != "" {
				label = theme.SubAgent + " " + shortID(msg.AgentID) + " · " + label
			}
			sb.WriteString(theme.Subtle.Render(theme.Sep + " " + label))
			sb.WriteString("\n")
			// Content is already previewed (truncated + pretty) at append time.
			wrapped := wrapText(msg.Content, contentWidth-2)
			for _, line := range strings.Split(strings.TrimRight(wrapped, "\n"), "\n") {
				sb.WriteString("  " + theme.Help.Render(line))
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
		case "error":
			prefix := errorStyle.Render(theme.Cross) + " "
			prefixWidth := lipgloss.Width(prefix)
			wrappedContent := wrapText(msg.Content, contentWidth-prefixWidth)
			// Add prefix to first line, indent continuation lines
			lines := strings.Split(strings.TrimRight(wrappedContent, "\n"), "\n")
			for i, line := range lines {
				if i == 0 {
					// First line: prefix + content
					sb.WriteString(prefix)
					sb.WriteString(line)
				} else {
					// Continuation lines: indent with spaces only (no prefix)
					sb.WriteString(strings.Repeat(" ", prefixWidth))
					sb.WriteString(line)
				}
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
		}
	}

	if m.loading {
		displayStatus := m.status
		if displayStatus == "" || displayStatus == "Thinking..." {
			displayStatus = theme.Status(theme.VerbThinking, m.statusPhase)
		}
		sb.WriteString(theme.SepStyle.Render(theme.Sep) + " " + theme.Reasoning.Render(displayStatus))
		sb.WriteString("\n")
		if m.reasoning != "" {
			wrapped := wrapText(m.reasoning, contentWidth-4)
			for _, line := range strings.Split(strings.TrimRight(wrapped, "\n"), "\n") {
				sb.WriteString("  " + theme.Reasoning.Render(line) + "\n")
			}
		}
	}

	if m.awaitingApproval && m.pendingTool != nil {
		gutter := theme.Gutter.Render(theme.ApprovalGutter) + " "
		sb.WriteString(gutter + theme.ApproveKey.Render(toolApprovalLabel(*m.pendingTool)))
		sb.WriteString("\n")
		args := wrapText(chat.FormatToolCall(m.pendingTool.Name, m.pendingTool.Arguments), contentWidth-4)
		for _, line := range strings.Split(strings.TrimRight(args, "\n"), "\n") {
			sb.WriteString(gutter + theme.Help.Render(line) + "\n")
		}
		if m.pendingTool.Reasoning != "" {
			rz := wrapText(m.pendingTool.Reasoning, contentWidth-4)
			for _, line := range strings.Split(strings.TrimRight(rz, "\n"), "\n") {
				sb.WriteString(gutter + theme.Reasoning.Render(line) + "\n")
			}
		}
		prompt := theme.ApprovePrompt
		if m.approvalEditing {
			prompt = theme.ApproveEditHint
		}
		sb.WriteString(gutter + theme.ApproveKey.Render(prompt))
		sb.WriteString("\n")
	}

	if m.awaitingAsk && m.pendingAsk != nil {
		sb.WriteString(renderAsk(*m.pendingAsk, m.width))
		sb.WriteString("\n")
	}

	m.viewport.SetContent(sb.String())
	m.viewport.GotoBottom()
}

// View renders the TUI.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var sb strings.Builder

	// Header: brand left, cwd right, one dim hairline beneath.
	brand := theme.Brand.Render(theme.BrandName)
	cwd := theme.Meta.Render(shortenPath(currentDir()))
	gap := m.width - lipgloss.Width(brand) - lipgloss.Width(cwd)
	if gap < 1 {
		gap = 1
	}
	sb.WriteString(brand + strings.Repeat(" ", gap) + cwd)
	sb.WriteString("\n")
	sb.WriteString(theme.Rule.Render(strings.Repeat("─", max(1, m.width))))
	sb.WriteString("\n")

	// Body: log viewer, first-run empty state, otherwise the conversation viewport.
	if m.showLogs {
		sb.WriteString(m.renderLogsViewer())
	} else if len(m.messages) == 0 && !m.loading && !m.awaitingApproval && !m.awaitingAsk {
		sb.WriteString(renderEmptyState(m.width))
	} else {
		sb.WriteString(m.viewport.View())
	}
	sb.WriteString("\n")

	// `/` completion popup, above the input.
	if comp := renderCompletion(m.completion, strings.TrimSpace(m.textarea.Value()), m.width); comp != "" {
		sb.WriteString(comp)
		sb.WriteString("\n")
	}

	// Input. In key-driven approval choice mode the textarea is hidden (the
	// approval block in the viewport carries the choice row); edit mode and
	// normal chat show the textarea.
	switch {
	case !m.sessionReady:
		sb.WriteString(theme.Help.Render(theme.Starting))
	case m.showLogs:
		// no input: the log viewer owns the body and the keystrokes
	case m.awaitingApproval && !m.approvalEditing:
		// no input: choice row lives in the viewport approval block
	default:
		sb.WriteString(m.textarea.View())
	}
	sb.WriteString("\n")
	help := theme.Help.Render(m.helpLine())
	if badge := m.contextBadge(); badge != "" {
		gap := m.width - lipgloss.Width(help) - lipgloss.Width(badge)
		if gap < 1 {
			gap = 1
		}
		sb.WriteString(help + strings.Repeat(" ", gap) + badge)
	} else {
		sb.WriteString(help)
	}

	if m.err != nil {
		sb.WriteString("\n" + theme.Error.Render(theme.Cross+" "+m.err.Error()))
	}

	// Jobs footers (renderers restyle internally; nil-safe when empty). Hidden
	// while the log viewer owns the body.
	if !m.showLogs {
		if f := renderJobsFooter(m.jobs, m.width); f != "" {
			sb.WriteString("\n" + f)
		}
		if f := renderShellJobsFooter(m.shellJobs.List(), m.width); f != "" {
			sb.WriteString("\n" + f)
		}
	}

	return sb.String()
}

// helpLine returns the context-appropriate help string.
func (m Model) helpLine() string {
	switch {
	case m.showLogs && m.logOpenID != "":
		return theme.ScrollKeys + " scroll · esc back · ctrl+o close"
	case m.showLogs:
		return theme.ScrollKeys + " select · enter open · k kill · esc close"
	case m.awaitingApproval && m.approvalEditing:
		return theme.HelpApprovalEdit
	case m.awaitingApproval:
		return theme.HelpApproval
	default:
		return theme.HelpDefault
	}
}

func currentDir() string {
	d, err := os.Getwd()
	if err != nil {
		return ""
	}
	return d
}

// shortenPath replaces the home-dir prefix with ~ for a compact header.
func shortenPath(p string) string {
	if p == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p == home {
			return "~"
		}
		if strings.HasPrefix(p, home+string(filepath.Separator)) {
			return "~" + p[len(home):]
		}
	}
	return p
}

// Output returns any command that should be output to the shell
func (m Model) Output() string {
	return m.output
}

// isWorking reports whether a turn or sub-agent is currently running.
func (m Model) isWorking() bool {
	if m.loading {
		return true
	}
	if m.session != nil && m.session.AgentManager() != nil {
		return m.session.AgentManager().HasRunning()
	}
	return false
}

// compactNotice formats a one-line compaction summary for the transcript.
func compactNotice(before, after int) string {
	return fmt.Sprintf("📦 Compacted conversation — %s → %s tokens", chat.HumanTokens(before), chat.HumanTokens(after))
}

// ctxBadgeWarn highlights the context badge once usage nears the auto-compaction
// threshold (clay — the palette's warmest attention color).
var ctxBadgeWarn = lipgloss.NewStyle().Foreground(theme.Accent)

// contextBadge renders the right-aligned context-size indicator for the bottom
// bar, e.g. "ctx 8k (6%)". It highlights once usage reaches the auto-compaction
// threshold. Returns "" when there's nothing to show yet.
func (m Model) contextBadge() string {
	used := m.contextTokens
	if used <= 0 {
		return ""
	}
	window := m.cfg.Compaction.MaxContextTokens
	if window <= 0 {
		// No configured window (auto-compaction off): show the bare size.
		return theme.Meta.Render("ctx " + chat.HumanTokens(used))
	}
	pct := used * 100 / window
	label := fmt.Sprintf("ctx %s (%d%%)", chat.HumanTokens(used), pct)
	threshold := m.cfg.Compaction.Threshold
	if threshold <= 0 || threshold > 1 {
		threshold = 0.8
	}
	if float64(used) >= float64(window)*threshold {
		return ctxBadgeWarn.Render(label)
	}
	return theme.Meta.Render(label)
}

// quit tears down the session and exits.
func (m Model) quit() (tea.Model, tea.Cmd) {
	m.quitting = true
	if m.session != nil {
		m.session.Close()
	}
	m.cancel()
	return m, tea.Quit
}
