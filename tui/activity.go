package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
)

// Activity chip kinds. A chip's kind decides what enter opens.
const (
	chipTodo   = "todo"
	chipShell  = "shell"
	chipAgent  = "agent"  // one running sub-agent; jobID names it
	chipAgents = "agents" // sub-agent history when none is running
	chipLoops  = "loops"
	chipGoal   = "goal"
)

// activityItem is one chip of the footer's activity strip, with what the
// focus mode needs to open it.
type activityItem struct {
	row   render.FooterRow
	kind  string
	jobID string
}

// unseenFailures is how many failures of kind the user has not looked at:
// failed now, less what was failed when they last opened the kind's view.
func (m Model) unseenFailures(kind string, failed int) int {
	return max(failed-m.seenFailed[kind], 0)
}

// alert renders n unseen failures as "✗n", or "" when there are none.
func alert(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("%s%d", theme.Cross, n)
}

// activityItems builds the activity strip: todo, shell and sub-agents always
// (so the line does not jump when work starts), then loops and the goal when
// there are any. Each running sub-agent has a chip of its own, so the user can
// open exactly the one they want to follow.
func (m Model) activityItems() []activityItem {
	var items []activityItem
	add := func(kind, jobID string, row render.FooterRow) {
		items = append(items, activityItem{row: row, kind: kind, jobID: jobID})
	}

	add(chipTodo, "", m.todoChip())

	var running, done, failed int
	for _, j := range m.shellJobs.List() {
		switch j.Status {
		case "running":
			running++
		case "completed":
			done++
		case "failed":
			failed++
		}
	}
	shell := render.FooterRow{Glyph: theme.ShellJob, Text: "shell " + theme.Idle, Kind: render.FooterShell,
		Alert: alert(m.unseenFailures(chipShell, failed))}
	switch {
	case running > 0:
		shell.Text, shell.State = fmt.Sprintf("shell %d running", running), render.ChipActive
	case done+failed > 0:
		shell.Text = fmt.Sprintf("shell %d done", done+failed)
	}
	add(chipShell, "", shell)

	failed, done = 0, 0
	for _, j := range m.jobs {
		switch j.Status {
		case chat.AgentStatusRunning:
			add(chipAgent, j.ID, render.FooterRow{Glyph: theme.SubAgent, Text: m.agentChipText(j), Kind: render.FooterJobs, State: render.ChipActive})
		case chat.AgentStatusFailed:
			failed++
		default:
			done++
		}
	}
	// The history chip stands in for sub-agents while none runs, and stays
	// beside running ones while a failure is unseen, so the alert never
	// sits on an agent that did not fail.
	agentAlert := alert(m.unseenFailures(chipAgents, failed))
	if len(items) == 2 || agentAlert != "" {
		text := "agents " + theme.Idle
		switch {
		case done > 0:
			text = fmt.Sprintf("agents %d done", done)
		case failed > 0:
			text = "agents"
		}
		add(chipAgents, "", render.FooterRow{Glyph: theme.SubAgent, Text: text, Kind: render.FooterJobs, Alert: agentAlert})
	}

	if n := m.loopCount(); n > 0 {
		add(chipLoops, "", render.FooterRow{Glyph: theme.Loop, Text: fmt.Sprintf("loops %d", n), Kind: render.FooterLoops, State: render.ChipActive})
	}
	if m.session != nil {
		if goal := m.session.Goal(); goal != "" {
			row := render.FooterRow{Glyph: theme.Goal, Text: "goal " + goal, Kind: render.FooterGoal, State: render.ChipActive}
			if m.session.GoalPaused() {
				row.Text, row.State = "goal paused "+theme.Sep+" "+goal, render.ChipIdle
			}
			add(chipGoal, "", row)
		}
	}

	if m.activityFocus && len(items) > 0 {
		items[min(m.activitySel, len(items)-1)].row.Selected = true
	}
	return items
}

// todoChip summarizes the todo list: the counts, then the item in progress
// (or the next one), which the presenter shortens first when space is tight.
func (m Model) todoChip() render.FooterRow {
	row := render.FooterRow{Glyph: theme.Todo, Text: "todo " + theme.Idle, Kind: render.FooterTodo}
	if m.session == nil || m.session.TodoList() == nil {
		return row
	}
	items := m.session.TodoList().Items()
	if len(items) == 0 {
		return row
	}
	done, total := m.session.TodoList().Counts()
	row.Text = fmt.Sprintf("todo %d/%d", done, total)
	var next string
	for _, it := range items {
		if it.Status == chat.TodoActive {
			next, row.State = it.Content, render.ChipActive
			break
		}
		if next == "" && it.Status != chat.TodoCompleted && it.Status != chat.TodoCancelled {
			next = it.Content
		}
	}
	if next != "" {
		row.Text += " " + next
	}
	return row
}

// agentChipText says which sub-agent this is and what it is doing: its type,
// a title from its task, its current step (the tool it called, or thinking /
// writing), and its output so far: "explore: scan the LoRA loader · bash ·
// 3.1k". The presenter shortens it from the end when space is tight.
func (m Model) agentChipText(j agentJob) string {
	typ := j.Type
	if typ == "" {
		typ = "agent"
	}
	parts := []string{typ}
	if title := j.title(); title != "" {
		parts[0] += ": " + title
	}
	doing := m.agentSpeed.doing(j.ID)
	r, ok := m.agentSpeed.read(j.ID, time.Now())
	switch {
	case doing != "":
		parts = append(parts, doing)
	case !ok || r.Total <= 0:
		parts = append(parts, "starting")
	}
	if ok && r.Total > 0 {
		parts = append(parts, chat.HumanTokens(int(math.Round(r.Total))))
	}
	return strings.Join(parts, " "+theme.Sep+" ")
}

// title names a sub-agent's work: the title the model wrote for it, or,
// until one arrives, its task's first sentence.
func (j agentJob) title() string {
	if j.Title != "" {
		return j.Title
	}
	return taskTitle(j.Task)
}

// maxTaskTitle caps a task title in runes, before the presenter's own fitting.
const maxTaskTitle = 40

// taskTitle makes a short title from a sub-agent's task: its first line, up
// to the end of the first sentence, capped at maxTaskTitle runes.
func taskTitle(task string) string {
	title, _, _ := strings.Cut(strings.TrimSpace(task), "\n")
	if i := strings.Index(title, ". "); i > 0 {
		title = title[:i]
	}
	title = strings.TrimSuffix(strings.TrimSpace(title), ".")
	return render.TruncateRunes(title, maxTaskTitle)
}

// loopCount is how many loops are scheduled, self-paced ones included.
func (m Model) loopCount() int {
	n := m.selfPaced
	if m.loops != nil {
		n += len(m.loops.List())
	}
	return n
}

// footerRows is the activity strip as the presenter draws it. Empty while a
// panel or the log viewer owns the body, which hides the strip.
func (m Model) footerRows() []render.FooterRow {
	if m.showLogs || m.panelOpen() {
		return nil
	}
	items := m.activityItems()
	rows := make([]render.FooterRow, len(items))
	for i, it := range items {
		rows[i] = it.row
	}
	return rows
}

// panelOpen reports whether a body-replacing panel (the todo list, or a loops
// or goal detail panel) is open.
func (m Model) panelOpen() bool {
	return m.showTodo || m.infoPanel != ""
}

// focusActivity puts the keyboard on the activity strip and selects the chip
// that most needs attention: an unseen failure, then running work, then the
// first chip.
func (m *Model) focusActivity() {
	items := m.activityItems()
	m.activityFocus, m.activitySel = true, 0
	for i, it := range items {
		if it.row.Alert != "" {
			m.activitySel = i
			return
		}
	}
	for i, it := range items {
		if it.row.State == render.ChipActive {
			m.activitySel = i
			return
		}
	}
}

// handleActivityKey handles a key while the activity strip has focus. It
// reports false for a key that leaves the strip and should reach the
// composer as usual, so typing is never lost.
func (m *Model) handleActivityKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	n := len(m.activityItems())
	switch msg.Type {
	case tea.KeyLeft, tea.KeyShiftTab:
		m.activitySel = (m.activitySel - 1 + n) % n
		return nil, true
	case tea.KeyRight, tea.KeyTab:
		m.activitySel = (m.activitySel + 1) % n
		return nil, true
	case tea.KeyEsc, tea.KeyCtrlG:
		m.activityFocus = false
		m.reflowLayout()
		return nil, true
	case tea.KeyEnter:
		m.activityFocus = false
		m.openActivity(m.activityItems()[min(m.activitySel, n-1)])
		m.reflowLayout()
		return nil, true
	}
	m.activityFocus = false
	m.reflowLayout()
	return nil, false
}

// openActivity opens the detail view for a chip: the todo panel, the log
// viewer on the right job, or the loops or goal panel.
func (m *Model) openActivity(it activityItem) {
	switch it.kind {
	case chipTodo:
		m.showTodo = true
	case chipAgent:
		m.openLogs("agent", it.jobID)
	case chipAgents:
		m.openLogs("agent", "")
	case chipShell:
		m.openLogs("shell", "")
	case chipLoops, chipGoal:
		m.infoPanel = it.kind
	}
}

// openLogs opens the log viewer. With an id it opens that job's log straight
// away; otherwise it selects the job of kind most worth a look (running, then
// failed, then the newest) in the list. Opening marks the kind's failures seen.
func (m *Model) openLogs(kind, id string) {
	m.markFailuresSeen()
	m.showLogs, m.logSel, m.logOpenID, m.logOpenKind = true, 0, "", ""
	jobs := m.unifiedJobs()
	if id != "" {
		for i, j := range jobs {
			if j.ID == id && j.Kind == kind {
				m.logSel = i
				m.openJobLog(j)
				return
			}
		}
	}
	best, rank := -1, 0
	for i, j := range jobs {
		if j.Kind != kind {
			continue
		}
		r := 1 // newest wins among equals: later jobs replace earlier ones
		switch j.Status {
		case "running":
			r = 3
		case "failed":
			r = 2
		}
		if r >= rank {
			best, rank = i, r
		}
	}
	if best >= 0 {
		m.logSel = best
	}
}

// markFailuresSeen records the failures on screen as looked at, which clears
// the chips' alerts until something else fails.
func (m *Model) markFailuresSeen() {
	if m.seenFailed == nil {
		m.seenFailed = map[string]int{}
	}
	shell := 0
	for _, j := range m.shellJobs.List() {
		if j.Status == "failed" {
			shell++
		}
	}
	agents := 0
	for _, j := range m.jobs {
		if j.Status == chat.AgentStatusFailed {
			agents++
		}
	}
	m.seenFailed[chipShell], m.seenFailed[chipAgents] = shell, agents
}

// renderInfoPanel renders the loops or goal detail panel, which replaces the
// body like the todo panel does.
func (m Model) renderInfoPanel() string {
	var b strings.Builder
	switch m.infoPanel {
	case chipLoops:
		b.WriteString(theme.Brand.Render("loops"))
		b.WriteString("\n\n")
		if m.loops != nil {
			for _, j := range m.loops.List() {
				state := ""
				if j.Paused {
					state = theme.Subtle.Render(" (paused)")
				}
				b.WriteString(theme.Running.Render(theme.Loop) + " " + theme.Meta.Render(j.ID+"  "+j.Expr) + state + "\n")
				b.WriteString("  " + j.Prompt + "\n")
			}
		}
		if m.selfPaced > 0 {
			b.WriteString(theme.Running.Render(theme.Loop) + " " + theme.Meta.Render(fmt.Sprintf("%d self-paced", m.selfPaced)) + "\n")
		}
		if m.loopCount() == 0 {
			b.WriteString(theme.Help.Render("No loops scheduled.") + "\n")
		}
		b.WriteString("\n" + theme.Help.Render("/loop list · /loop stop <id>"))
	case chipGoal:
		b.WriteString(theme.Brand.Render("goal"))
		b.WriteString("\n\n")
		goal, paused := "", false
		if m.session != nil {
			goal, paused = m.session.Goal(), m.session.GoalPaused()
		}
		if goal == "" {
			b.WriteString(theme.Help.Render("No goal set."))
			break
		}
		if paused {
			b.WriteString(theme.Subtle.Render("paused") + "\n\n")
		} else {
			b.WriteString(theme.Running.Render("active") + "\n\n")
		}
		b.WriteString(goal + "\n\n")
		if paused {
			b.WriteString(theme.Help.Render("/goal resume · /goal clear"))
		} else {
			b.WriteString(theme.Help.Render("/goal clear"))
		}
	}
	return b.String()
}
