package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/theme"
)

// baseApprovalMode is where the Shift+Tab cycle returns to: the configured
// mode when it is one that prompts, else prompt.
func baseApprovalMode(configured string) string {
	switch configured {
	case "strict", "allowlist", "prompt":
		return configured
	default:
		return "prompt"
	}
}

// nextApprovalMode is the mode Shift+Tab switches to from current: base,
// then classify (when a classifier is configured), then auto, then base.
func nextApprovalMode(current, base string, hasClassifier bool) string {
	switch current {
	case "auto":
		return base
	case "classify":
		return "auto"
	default:
		if hasClassifier {
			return "classify"
		}
		return "auto"
	}
}

// effectiveApprovalMode is the mode the user is in: auto while /yolo is on,
// else the session's approval_mode.
func effectiveApprovalMode(s *chat.Session) string {
	if s == nil {
		return "prompt"
	}
	if s.AutoApprove() {
		return "auto"
	}
	return s.ApprovalMode()
}

// setApprovalMode switches the session to mode for this session only, and
// reports the result in the transcript. An empty mode reports the current
// one.
func (m *Model) setApprovalMode(mode string) {
	if m.session == nil {
		return
	}
	if mode != "" {
		if err := m.session.SetApprovalMode(mode); err != nil {
			m.appendMessage(ChatMessage{Role: "error", Content: err.Error()})
			return
		}
	}
	m.appendMessage(ChatMessage{Role: "agent", Content: fmt.Sprintf(theme.ApproveModeNotice, effectiveApprovalMode(m.session))})
}

// cycleApprovalMode is Shift+Tab: the next mode after the current one.
func (m *Model) cycleApprovalMode() {
	if m.session == nil {
		return
	}
	next := nextApprovalMode(effectiveApprovalMode(m.session), baseApprovalMode(m.cfg.ApprovalMode), m.session.HasClassifier())
	m.setApprovalMode(next)
}

// autoApprovedMsg reports a call the classifier approved without a prompt.
type autoApprovedMsg struct {
	req     chat.ToolCallRequest
	verdict chat.Verdict
}

// autoApprovedLine is the transcript line for msg, so the user sees what the
// classifier let through and why.
func autoApprovedLine(msg autoApprovedMsg) string {
	call, _, _ := strings.Cut(chat.FormatToolCall(msg.req.Name, msg.req.Arguments), "\n")
	return fmt.Sprintf(theme.AutoApprovedNotice, msg.verdict.Category, msg.verdict.Confidence, call)
}

// listenAutoApproved waits for the next classifier approval.
func (m Model) listenAutoApproved() tea.Cmd {
	return func() tea.Msg {
		select {
		case msg := <-m.autoApprovedChan:
			return msg
		case <-m.ctx.Done():
			return nil
		}
	}
}
