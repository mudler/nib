package tui

import (
	"fmt"
	"github.com/mudler/nib/chat"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/tui/render"
	"strings"
	"time"
)

type phaseState uint8

const (
	phaseReady phaseState = iota + 1
	phaseWorking
	phaseRunning
	phaseWaiting
	phaseApproval
	phaseParked
	phaseInterrupting
)

type phaseIdentity struct {
	state phaseState
	tool  string
	count int
}

func (m Model) currentActivityPhase() phaseIdentity {
	switch {
	case m.interruptArmed:
		return phaseIdentity{state: phaseInterrupting}
	case m.awaitingApproval:
		return phaseIdentity{state: phaseApproval}
	case m.awaitingAsk:
		return phaseIdentity{state: phaseWaiting}
	case len(m.running) > 0:
		p := phaseIdentity{state: phaseRunning, count: len(m.running)}
		if len(m.running) == 1 {
			p.tool = render.TruncateRunes(strings.TrimSpace(strings.Join(strings.Fields(m.running[0].name), " ")), 40)
		}
		return p
	case m.loading:
		return phaseIdentity{state: phaseWorking}
	case m.parked:
		return phaseIdentity{state: phaseParked}
	default:
		return phaseIdentity{state: phaseReady}
	}
}

func (m *Model) syncActivityPhase(now time.Time) {
	phase := m.currentActivityPhase()
	if phase == m.activityPhase {
		return
	}
	m.activityPhase = phase
	m.phaseStartedAt = now
}

func textReceiptAge(s receiptState, now time.Time) string {
	// The accepted callback contract has incomplete text attribution, even
	// in fresh epochs. Absence cannot establish "not yet received".
	if s.Text.IsZero() {
		return "last model text age: unknown"
	}
	return fmt.Sprintf("last model text received %ds ago", receiptSeconds(now, s.Text))
}
func receiptSeconds(now, then time.Time) int64 { return max(0, int64(now.Sub(then)/time.Second)) }
func observationAge(s receiptState, now time.Time) string {
	if s.Latest.Received.IsZero() {
		return textReceiptAge(s, now)
	}
	if s.Latest.HasText {
		return textReceiptAge(s, now)
	}
	// Never render arbitrary callback strings as labels.
	label := "event received"
	switch s.Latest.Kind {
	case "parked", "resumed", "tool started", "tool completed", "tool requested", "tool-call fragment received", "stream event received":
		label = s.Latest.Kind
	}
	return fmt.Sprintf("%s %ds ago", label, receiptSeconds(now, s.Latest.Received))
}

// activitySummary reads authoritative UI lifecycle state. Status strings and
// receipt metadata cannot establish a phase or clear an approval/cancellation.
func (m Model) activitySummary(now time.Time) render.ActivitySummary {
	phase := m.currentActivityPhase()
	s := render.ActivitySummary{Primary: "Ready", Compact: "Ready"}
	switch phase.state {
	case phaseInterrupting:
		s.Primary, s.Compact = "Interrupting", "Interrupting"
	case phaseApproval:
		s.Primary, s.Compact = "Approval needed", "Approval"
	case phaseWaiting:
		s.Primary, s.Compact = "Waiting for your answer", "Answer needed"
	case phaseRunning:
		s.Primary = fmt.Sprintf("%d tools active", phase.count)
		s.Compact = "Tools active"
		if phase.count == 1 {
			s.Primary = "Running tool"
			s.Compact = "Running"
			if phase.tool != "" {
				s.Primary = "Running " + phase.tool
			}
		}
	case phaseWorking:
		s.Primary, s.Compact = "Working", "Working"
	case phaseParked:
		s.Primary, s.Compact = "Parked", "Parked"
	}
	if phase.state != phaseReady && phase == m.activityPhase && !m.phaseStartedAt.IsZero() {
		elapsed := now.Sub(m.phaseStartedAt)
		if elapsed < 0 {
			elapsed = 0
		}
		s.Secondary = humanAge(elapsed)
	}
	s.Counts = backgroundCounts(m.jobs, m.shellJobs.List())
	return s
}

func backgroundCounts(jobs []agentJob, shellJobs []wizmcp.ShellJobInfo) string {
	agents, shell := 0, 0
	for _, j := range jobs {
		if j.Background && j.Status == chat.AgentStatusRunning {
			agents++
		}
	}
	{
		for _, j := range shellJobs {
			if j.Running && j.Backgrounded {
				shell++
			}
		}
	}
	if agents+shell > 0 {
		return fmt.Sprintf("background: agents %d, shell %d", agents, shell)
	}
	return ""
}

// receiptDetails keeps event and model-text clocks separate in existing logs.
func receiptDetails(s receiptState, now time.Time) string {
	event := "latest event age: unknown"
	if !s.Latest.Received.IsZero() {
		event = "latest event: " + observationAge(s, now)
		if s.Latest.HasText {
			event = fmt.Sprintf("latest event: model text received %ds ago", receiptSeconds(now, s.Latest.Received))
		}
	}
	return event + "\n" + textReceiptAge(s, now)
}
