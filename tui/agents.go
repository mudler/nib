package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/theme"
)

// agentTranscriptLine renders a durable one-line transcript marker for a
// sub-agent lifecycle event, or "" for statuses that should not be logged.
func agentTranscriptLine(ev chat.AgentEvent) string {
	typ := ev.Type
	if typ == "" {
		typ = "agent"
	}
	switch ev.Status {
	case chat.AgentStatusRunning:
		if ev.Task != "" {
			return fmt.Sprintf("sub-agent %s started: %s", typ, ev.Task)
		}
		return fmt.Sprintf("sub-agent %s started", typ)
	case chat.AgentStatusCompleted:
		return fmt.Sprintf("sub-agent %s finished", typ)
	case chat.AgentStatusFailed:
		if ev.Err != nil {
			return fmt.Sprintf("sub-agent %s failed: %v", typ, ev.Err)
		}
		return fmt.Sprintf("sub-agent %s failed", typ)
	default:
		return ""
	}
}

// agentJob is the UI view of a sub-agent for the jobs footer.
type agentJob struct {
	ID     string
	Type   string
	Task   string
	Status chat.AgentStatus
}

var jobsFooterStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

// renderJobsFooter renders a compact one-line summary of active jobs.
// Returns "" when there are no jobs so the footer takes no vertical space.
func renderJobsFooter(jobs []agentJob, width int) string {
	if len(jobs) == 0 {
		return ""
	}
	var running, done, failed int
	for _, j := range jobs {
		switch j.Status {
		case chat.AgentStatusRunning:
			running++
		case chat.AgentStatusCompleted:
			done++
		case chat.AgentStatusFailed:
			failed++
		}
	}
	parts := []string{fmt.Sprintf("jobs: %d running", running)}
	if done > 0 {
		parts = append(parts, fmt.Sprintf("%d done", done))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	parts = append(parts, "(ctrl+b background · ctrl+j detail · ctrl+k kill)")
	line := strings.Join(parts, "  ·  ")
	return jobsFooterStyle.Width(width).Render(line)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// toolApprovalLabel builds the tool-approval header, labeling sub-agent calls.
func toolApprovalLabel(req chat.ToolCallRequest) string {
	if req.AgentID != "" {
		return fmt.Sprintf("%s %s · run: %s", theme.SubAgent, shortID(req.AgentID), req.Name)
	}
	return fmt.Sprintf("run: %s", req.Name)
}

// firstRunningJobID returns the id of the first running job, or "".
func (m Model) firstRunningJobID() string {
	for _, j := range m.jobs {
		if j.Status == chat.AgentStatusRunning {
			return j.ID
		}
	}
	return ""
}

// applyAgentEvent upserts a job by ID and refreshes status.
func (m *Model) applyAgentEvent(ev chat.AgentEvent) {
	for i := range m.jobs {
		if m.jobs[i].ID == ev.ID {
			m.jobs[i].Status = ev.Status
			if ev.Type != "" {
				m.jobs[i].Type = ev.Type
			}
			return
		}
	}
	m.jobs = append(m.jobs, agentJob{ID: ev.ID, Type: ev.Type, Task: ev.Task, Status: ev.Status})
}
