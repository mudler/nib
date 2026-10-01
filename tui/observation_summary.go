package tui

import (
	"fmt"
	"github.com/mudler/nib/chat"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/tui/render"
	"strings"
	"time"
)

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
	s := render.ActivitySummary{Primary: "Ready", Compact: "Ready", Secondary: observationAge(m.toolEvents.rootObservation(), now)}
	switch {
	case m.interruptArmed:
		s.Primary, s.Compact = "Interrupting foreground", "Interrupting"
	case m.awaitingApproval:
		s.Primary, s.Compact = "Approval needed", "Approval"
	case m.awaitingAsk:
		s.Primary, s.Compact = "Waiting for your answer", "Answer needed"
	// No current callback supplies scoped operational notice start/end evidence.
	// Exclude status strings instead of interpreting them as phases.
	case len(m.running) > 0:
		s.Primary = fmt.Sprintf("%d tools active", len(m.running))
		s.Compact = "Tools active"
		if len(m.running) == 1 {
			s.Primary = "Running tool"
			if name := strings.TrimSpace(m.running[0].name); name != "" {
				s.Primary = "Running " + render.TruncateRunes(strings.Join(strings.Fields(name), " "), 40)
			}
		}
	case m.loading:
		s.Primary, s.Compact = "Working", "Working"
	case m.parked:
		s.Primary, s.Compact = "Parked", "Parked"
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
