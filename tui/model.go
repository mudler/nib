package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mudler/wiz/types"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/wiz/chat"
	"github.com/mudler/wiz/slash"
)

// ChatMessage represents a message in the chat history
type ChatMessage struct {
	Role    string
	Content string
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

	// ask_user state
	pendingAsk      *chat.AskRequest
	awaitingAsk     bool
	askRequestChan  chan chat.AskRequest
	askResponseChan chan string

	// Animation state
	statusPhase int

	// Sub-agent jobs state
	jobs           []agentJob
	showJobsDetail bool
	agentEventChan chan chat.AgentEvent

	// Unified `/` completion state
	completion compState

	// Channels for async communication with callbacks
	statusChan       chan string
	reasoningChan    chan string
	toolRequestChan  chan chat.ToolCallRequest
	toolResponseChan chan chat.ToolCallResponse
}

// responseMsg is sent when the AI responds
type responseMsg struct {
	content string
	err     error
}

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

// sessionReadyMsg is sent when the session is initialized
type sessionReadyMsg struct {
	session *chat.Session
	err     error
}

// NewModel creates a new TUI model
func NewModel(ctx context.Context, cfg types.Config, height int, transports ...mcp.Transport) Model {
	ctx, cancel := context.WithCancel(ctx)

	ta := textarea.New()
	ta.Placeholder = "Ask the wizard..."
	ta.Focus()
	ta.Prompt = "│ "
	ta.CharLimit = 4096
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(false) // Enter sends message

	vp := viewport.New(80, 10)
	vp.SetContent("✨ Welcome! The wizard awaits your command.\n\nType your question and press Enter. Press Esc to exit.")

	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))

	// Calculate max height - negative means percentage, positive means lines
	maxH := height
	if maxH < 0 {
		maxH = 0 // Will be calculated on first WindowSizeMsg
	}

	m := Model{
		viewport:         vp,
		textarea:         ta,
		spinner:          s,
		messages:         []ChatMessage{},
		ctx:              ctx,
		cancel:           cancel,
		maxHeight:        maxH,
		transports:       transports,
		cfg:              cfg,
		height:           height,
		agentEventChan:   make(chan chat.AgentEvent, 16),
		statusChan:       make(chan string, 10),
		reasoningChan:    make(chan string, 10),
		toolRequestChan:  make(chan chat.ToolCallRequest),
		toolResponseChan: make(chan chat.ToolCallResponse),
		askRequestChan:   make(chan chat.AskRequest),
		askResponseChan:  make(chan string),
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
			OnAgentEvent: func(ev chat.AgentEvent) {
				select {
				case m.agentEventChan <- ev:
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

		case tea.KeyCtrlB:
			// Background (detach) the first running foreground sub-agent.
			if m.sessionReady && m.session != nil {
				if id := m.firstRunningJobID(); id != "" {
					_ = m.session.AgentManager().Detach(id)
				}
			}
			return m, nil

		case tea.KeyCtrlJ:
			// Toggle the expanded jobs detail list.
			m.showJobsDetail = !m.showJobsDetail
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
			default: // slash.KindSend
				m.loading = true
				m.interruptArmed = false
				m.status = "Thinking..."
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
		cmds = append(cmds, m.listenStatus(), m.listenReasoning(), m.listenToolRequest(), m.listenAskRequest(), m.listenAgentEvents())

	case responseMsg:
		m.loading = false
		m.interruptArmed = false
		m.status = ""
		m.reasoning = ""
		if msg.err != nil {
			if errors.Is(msg.err, context.Canceled) {
				m.messages = append(m.messages, ChatMessage{Role: "agent", Content: "⛔ Interrupted."})
			} else {
				m.err = msg.err
				m.messages = append(m.messages, ChatMessage{Role: "error", Content: msg.err.Error()})
			}
		} else {
			m.messages = append(m.messages, ChatMessage{Role: "assistant", Content: msg.content})
		}
		m.updateViewport()

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
		m.loading = false  // Allow user input for approval
		m.textarea.Focus() // Ensure textarea is focused for input
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
		// Durable transcript marker so sub-agent activity stays visible in history.
		if line := agentTranscriptLine(ev); line != "" {
			m.messages = append(m.messages, ChatMessage{Role: "agent", Content: line})
		}
		m.updateViewport()
		// Continue listening for more agent events
		cmds = append(cmds, m.listenAgentEvents())

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

// handleToolApproval handles tool approval input
func (m Model) handleToolApproval(input string) (tea.Model, tea.Cmd) {
	input = strings.ToLower(strings.TrimSpace(input))

	var response chat.ToolCallResponse
	switch input {
	case "y", "yes":
		response = chat.ToolCallResponse{Approved: true}
	case "a", "always":
		response = chat.ToolCallResponse{Approved: true, AlwaysAllow: true}
	case "n", "no":
		response = chat.ToolCallResponse{Approved: false}
	default:
		// Treat as adjustment
		response = chat.ToolCallResponse{Approved: true, Adjustment: input}
	}

	m.awaitingApproval = false
	m.pendingTool = nil
	m.textarea.Reset()
	m.loading = true
	m.status = "Executing tool..."
	m.updateViewport()

	// Send response back to the waiting callback
	return m, func() tea.Msg {
		m.toolResponseChan <- response
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
	footerHeight := 5 // textarea + border
	statusHeight := 1

	vpHeight := effectiveHeight - headerHeight - footerHeight - statusHeight
	if vpHeight < 5 {
		vpHeight = 5
	}

	m.viewport.Width = m.width
	m.viewport.Height = vpHeight
	m.textarea.SetWidth(m.width - 2)
}

// Wizard face animation frames - the wizard winks while thinking!
var wizardFaces = []string{
	"◠ ◠", // normal
	"◠ ─", // wink right
	"◠ ◠", // normal
	"─ ◠", // wink left
	"─ ─", // blink
	"◠ ◠", // normal
}

// Sparkle animation for the header
var wizardSparkles = []string{"✨", "⭐", "💫", "✨", "⭐", "💫"}

// getWizardFace returns the current wizard face animation frame
func (m *Model) getWizardFace() string {
	return wizardFaces[m.statusPhase%len(wizardFaces)]
}

// getWizardSparkle returns the current sparkle animation
func (m *Model) getWizardSparkle() string {
	return wizardSparkles[m.statusPhase%len(wizardSparkles)]
}

// getThinkingStatus returns an animated thinking status message
func (m *Model) getThinkingStatus() string {
	phases := []string{
		"Casting spell",
		"Casting spell.",
		"Casting spell..",
		"Casting spell...",
		"Conjuring",
		"Conjuring.",
		"Conjuring..",
		"Conjuring...",
		"Summoning wisdom",
		"Summoning wisdom.",
		"Summoning wisdom..",
		"Summoning wisdom...",
	}
	return phases[m.statusPhase%len(phases)]
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

			// If a single word is longer than width, we have to break it
			if wordWidth > width && currentWidth == 0 {
				// Break the word itself (simple approach: just truncate with ellipsis)
				if width > 3 {
					result.WriteString(word[:width-3])
					result.WriteString("...")
				} else {
					result.WriteString(word[:width])
				}
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

// updateViewport updates the viewport content with chat messages
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
			prefix := userStyle.Render("👤 You: ")
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
			prefix := assistantStyle.Render("🧙 Wiz: ")
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
		case "agent":
			prefix := agentStyle.Render("🤖 ")
			prefixWidth := lipgloss.Width(prefix)
			wrappedContent := wrapText(msg.Content, contentWidth-prefixWidth)
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
		case "error":
			prefix := errorStyle.Render("✗ Error: ")
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
		// Use animated status if no specific status is set
		displayStatus := m.status
		if displayStatus == "" || displayStatus == "Thinking..." {
			displayStatus = m.getThinkingStatus()
		}

		// Build thinking box content
		// Account for box padding (1 char each side) and border (1 char each side) = 4 chars total
		boxContentWidth := contentWidth - 4
		if boxContentWidth < 20 {
			boxContentWidth = 20 // minimum width
		}

		var thinkingContent strings.Builder
		thinkingContent.WriteString(thinkingStyle.Render(m.spinner.View() + " " + displayStatus))
		if m.reasoning != "" {
			thinkingContent.WriteString("\n")
			reasoningPrefix := reasoningStyle.Render("💭 ")
			reasoningPrefixWidth := lipgloss.Width(reasoningPrefix)
			wrappedReasoning := wrapText(m.reasoning, boxContentWidth-reasoningPrefixWidth)
			reasoningLines := strings.Split(strings.TrimRight(wrappedReasoning, "\n"), "\n")
			for i, line := range reasoningLines {
				if i == 0 {
					thinkingContent.WriteString(reasoningPrefix)
					thinkingContent.WriteString(line)
				} else {
					thinkingContent.WriteString(strings.Repeat(" ", reasoningPrefixWidth))
					thinkingContent.WriteString(line)
				}
				thinkingContent.WriteString("\n")
			}
		}

		sb.WriteString(thinkingBoxStyle.Render(thinkingContent.String()))
		sb.WriteString("\n")
	}

	if m.awaitingApproval && m.pendingTool != nil {
		// Build tool request box content
		// Account for box padding (1 char each side) and border (1 char each side) = 4 chars total
		boxContentWidth := contentWidth - 4
		if boxContentWidth < 20 {
			boxContentWidth = 20 // minimum width
		}

		var toolContent strings.Builder
		toolContent.WriteString(toolNameStyle.Render(toolApprovalLabel(*m.pendingTool)))
		toolContent.WriteString("\n\n")
		// Wrap arguments
		argsPrefix := dimmedStyle.Render("Arguments: ")
		argsPrefixWidth := lipgloss.Width(argsPrefix)
		wrappedArgs := wrapText(m.pendingTool.Arguments, boxContentWidth-argsPrefixWidth)
		argsLines := strings.Split(strings.TrimRight(wrappedArgs, "\n"), "\n")
		for i, line := range argsLines {
			if i == 0 {
				toolContent.WriteString(argsPrefix)
				toolContent.WriteString(line)
			} else {
				toolContent.WriteString(strings.Repeat(" ", argsPrefixWidth))
				toolContent.WriteString(line)
			}
			toolContent.WriteString("\n")
		}
		if m.pendingTool.Reasoning != "" {
			toolContent.WriteString("\n")
			reasoningPrefix := reasoningStyle.Render("💭 ")
			reasoningPrefixWidth := lipgloss.Width(reasoningPrefix)
			wrappedReasoning := wrapText(m.pendingTool.Reasoning, boxContentWidth-reasoningPrefixWidth)
			reasoningLines := strings.Split(strings.TrimRight(wrappedReasoning, "\n"), "\n")
			for i, line := range reasoningLines {
				if i == 0 {
					toolContent.WriteString(reasoningPrefix)
					toolContent.WriteString(line)
				} else {
					toolContent.WriteString(strings.Repeat(" ", reasoningPrefixWidth))
					toolContent.WriteString(line)
				}
				toolContent.WriteString("\n")
			}
		}
		toolContent.WriteString("\n")
		toolContent.WriteString(promptHintStyle.Render("[y]es  [a]lways  [n]o  "))
		toolContent.WriteString(dimmedStyle.Render("or type adjustment"))

		sb.WriteString(toolRequestBoxStyle.Render(toolContent.String()))
		sb.WriteString("\n")
	}

	if m.awaitingAsk && m.pendingAsk != nil {
		sb.WriteString(renderAsk(*m.pendingAsk, m.width))
		sb.WriteString("\n")
	}

	m.viewport.SetContent(sb.String())
	m.viewport.GotoBottom()
}

// View renders the TUI
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var sb strings.Builder

	// Header with animated wizard
	sparkle := m.getWizardSparkle()
	if m.loading {
		// Animated wizard face when loading
		face := m.getWizardFace()
		sb.WriteString(headerStyle.Render(fmt.Sprintf("%s [%s] wiz", sparkle, face)))
	} else {
		sb.WriteString(headerStyle.Render(fmt.Sprintf("%s [◠ ◠] wiz", sparkle)))
	}
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("─", m.width))
	sb.WriteString("\n")

	// Chat viewport
	sb.WriteString(m.viewport.View())
	sb.WriteString("\n")

	// Separator
	sb.WriteString(strings.Repeat("─", m.width))
	sb.WriteString("\n")

	// `/` completion popup (above the input)
	if comp := renderCompletion(m.completion, strings.TrimSpace(m.textarea.Value()), m.width); comp != "" {
		sb.WriteString(comp)
		sb.WriteString("\n")
	}

	// Input area
	if m.sessionReady {
		sb.WriteString(m.textarea.View())
	} else {
		sb.WriteString(m.spinner.View() + " Summoning the wizard...")
	}

	// Help text
	sb.WriteString("\n")
	helpText := "Enter: send • Ctrl+C: interrupt/exit • Esc: exit"
	sb.WriteString(helpStyle.Render(helpText))

	if m.err != nil {
		sb.WriteString("\n")
		sb.WriteString(errorStyle.Render(fmt.Sprintf("Error: %v", m.err)))
	}

	// Sub-agent jobs footer (and optional detail). Adds nothing when no jobs,
	// so the layout is unchanged in the common case.
	footer := renderJobsFooter(m.jobs, m.width)
	if m.showJobsDetail {
		if d := renderJobsDetail(m.jobs, m.width); d != "" {
			if footer != "" {
				footer = d + "\n" + footer
			} else {
				footer = d
			}
		}
	}
	if footer != "" {
		sb.WriteString("\n")
		sb.WriteString(footer)
	}

	return sb.String()
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

// quit tears down the session and exits.
func (m Model) quit() (tea.Model, tea.Cmd) {
	m.quitting = true
	if m.session != nil {
		m.session.Close()
	}
	m.cancel()
	return m, tea.Quit
}
