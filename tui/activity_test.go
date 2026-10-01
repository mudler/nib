package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/loop"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/types"
)

const testChipHistory = "history"

func kt(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

func stripKeys(t *testing.T, m Model, msgs ...tea.KeyMsg) Model {
	t.Helper()
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

func chipTexts(m Model) []string {
	var out []string
	for _, it := range m.activityItems() {
		out = append(out, it.row.Text+it.row.Alert)
	}
	return out
}

// Each running sub-agent gets a chip of its own; empty history is omitted.
func TestActivityChipsPerRunningAgent(t *testing.T) {
	m := newQueueTestModel()
	if got := strings.Join(chipTexts(m), " | "); got != "todo –" {
		t.Fatalf("idle strip = %q", got)
	}
	m.jobs = []agentJob{
		{ID: "a1", Type: "explore", Task: "Scan the LoRA loader. Then report back.", Status: chat.AgentStatusRunning},
		{ID: "a2", Type: "plan", Task: "draft", Status: chat.AgentStatusRunning},
	}
	got := chipTexts(m)
	if len(got) != 3 || got[1] != "explore: Scan the LoRA loader · starting" || got[2] != "plan: draft · starting" {
		t.Fatalf("strip = %q, want one titled chip per running agent and no history chip", got)
	}
}

// A sub-agent's chip says what it is doing: the tool it called last, or
// thinking / writing while it streams text.
func TestAgentChipShowsItsStep(t *testing.T) {
	m := newQueueTestModel()
	m.agentSpeed = &agentMeters{}
	m.jobs = []agentJob{{ID: "a1", Type: "explore", Task: "scan", Status: chat.AgentStatusRunning}}

	m.agentSpeed.step("a1", chat.StreamEvent{Kind: "tool_call", ToolName: "bash", AgentID: "a1"})
	if got := m.agentChipText(m.jobs[0]); !strings.Contains(got, "bash") {
		t.Fatalf("chip = %q, want the tool it called", got)
	}
	m.agentSpeed.step("a1", chat.StreamEvent{Kind: "reasoning", Content: "hmm", AgentID: "a1"})
	m.agentSpeed.record("a1", 4000, time.Now())
	got := m.agentChipText(m.jobs[0])
	if !strings.Contains(got, "thinking") || !strings.Contains(got, "1k") {
		t.Fatalf("chip = %q, want the step and the output so far", got)
	}
}

// An unseen failure is on the history chip, never on an agent that is still
// running, and opening the log viewer marks it seen.
func TestFailureAlertIsOnTheHistoryChip(t *testing.T) {
	m := newQueueTestModel()
	m.jobs = []agentJob{
		{ID: "a1", Type: "explore", Status: chat.AgentStatusRunning},
		{ID: "a2", Type: "edit", Status: chat.AgentStatusFailed},
	}
	items := m.activityItems()
	last := items[len(items)-1]
	if last.kind != chipHistory || last.row.Alert == "" {
		t.Fatalf("last chip = %+v, want the history chip carrying the alert", last)
	}
	for _, it := range items {
		if it.kind == chipAgent && it.row.Alert != "" {
			t.Fatalf("running agent chip %q carries the alert", it.row.Text)
		}
	}

	m = stripKeys(t, m, kt(tea.KeyCtrlO), kt(tea.KeyCtrlO))
	for _, it := range m.activityItems() {
		if it.row.Alert != "" {
			t.Fatalf("after opening the logs, strip still has an alert: %+v", it)
		}
	}
}

func TestActivityHistoryUnifiesTerminalJobsAndPersistsAfterSeen(t *testing.T) {
	m := newQueueTestModel()
	m.jobs = []agentJob{
		{ID: "running", Type: "explore", Status: chat.AgentStatusRunning},
		{ID: "done", Type: "plan", Status: chat.AgentStatusCompleted},
		{ID: "failed", Type: "edit", Status: chat.AgentStatusFailed},
	}

	items := m.activityItems()
	var histories []activityItem
	for _, it := range items {
		if it.kind == testChipHistory {
			histories = append(histories, it)
		}
		if strings.HasPrefix(it.row.Text, "agents ") || strings.Contains(it.row.Text, " done") {
			t.Fatalf("legacy terminal-work chip remains: %+v", it)
		}
	}
	if len(histories) != 1 || histories[0].row.Text != "History 2" || histories[0].row.Alert != "×1" {
		t.Fatalf("history = %+v, want one History 2 ×1", histories)
	}

	m.openActivity(histories[0])
	if !m.showLogs || m.logOpenID != "" {
		t.Fatalf("history should open the unified list, showLogs=%v openID=%q", m.showLogs, m.logOpenID)
	}
	items = m.activityItems()
	found := false
	for _, it := range items {
		if it.kind == testChipHistory {
			found = true
			if it.row.Text != "History 2" || it.row.Alert != "" {
				t.Fatalf("seen history = %+v, want retained records without alert", it.row)
			}
		}
	}
	if !found {
		t.Fatal("marking failures seen removed history")
	}
}

func TestOnlyOpeningHistoryMarksFailuresSeen(t *testing.T) {
	m := newQueueTestModel()
	m.jobs = []agentJob{
		{ID: "live", Type: "explore", Status: chat.AgentStatusRunning},
		{ID: "failed", Type: "edit", Status: chat.AgentStatusFailed},
	}

	m.openLogs("agent", "live")
	if got := historyAlert(m); got != "×1" {
		t.Fatalf("opening a live agent cleared history alert: %q", got)
	}
	m.showLogs = false
	m.openLogs("shell", "")
	if got := historyAlert(m); got != "×1" {
		t.Fatalf("opening live shell routing cleared history alert: %q", got)
	}
	m.showLogs = false
	m.openActivity(activityItem{kind: chipHistory})
	if got := historyAlert(m); got != "" {
		t.Fatalf("opening History left alert %q", got)
	}
}

func TestSeenHistoryTracksFailureIdentityAcrossRetention(t *testing.T) {
	m := newQueueTestModel()
	m.jobs = []agentJob{{ID: "old", Status: chat.AgentStatusFailed}}
	m.openActivity(activityItem{kind: chipHistory})
	if got := historyAlert(m); got != "" {
		t.Fatalf("old failure remained unseen: %q", got)
	}

	// Retention evicts the failure the user saw, then a distinct failure arrives.
	m.jobs = []agentJob{{ID: "new", Status: chat.AgentStatusFailed}}
	if got := historyAlert(m); got != "×1" {
		t.Fatalf("new failure after eviction alert = %q, want ×1", got)
	}
}

func TestCtrlGSelectsUnseenHistoryBeforeLiveWorkAndEnterOpensUnifiedList(t *testing.T) {
	m := newQueueTestModel()
	m.jobs = []agentJob{
		{ID: "live", Type: "explore", Status: chat.AgentStatusRunning},
		{ID: "failed", Type: "edit", Status: chat.AgentStatusFailed},
	}
	m = stripKeys(t, m, kt(tea.KeyCtrlG))
	if got := m.activityItems()[m.activitySel].kind; got != chipHistory {
		t.Fatalf("ctrl+g selected %q, want unseen History", got)
	}
	m = stripKeys(t, m, kt(tea.KeyEnter))
	if !m.showLogs || m.logOpenID != "" || m.logOpenKind != "" {
		t.Fatalf("History Enter did not open unified list: show=%v id=%q kind=%q", m.showLogs, m.logOpenID, m.logOpenKind)
	}
	jobs := m.unifiedJobs()
	if len(jobs) != 2 || jobs[0].ID != "live" || jobs[1].ID != "failed" {
		t.Fatalf("unified ordering = %+v", jobs)
	}
}

func historyAlert(m Model) string {
	for _, it := range m.activityItems() {
		if it.kind == chipHistory {
			return it.row.Alert
		}
	}
	return ""
}

func TestActivityOmitsEmptyHistory(t *testing.T) {
	m := newQueueTestModel()
	m.jobs = []agentJob{{ID: "running", Type: "explore", Status: chat.AgentStatusRunning}}
	for _, it := range m.activityItems() {
		if it.kind == testChipHistory || strings.HasPrefix(it.row.Text, "History ") {
			t.Fatalf("empty history rendered: %+v", it)
		}
	}
}

func TestHistoryCountsMixedTerminalJobsOnly(t *testing.T) {
	agents := []agentJob{
		{Status: chat.AgentStatusRunning},
		{Status: chat.AgentStatusCompleted},
		{Status: chat.AgentStatusFailed},
	}
	shells := []wizmcp.ShellJobInfo{
		{Status: "running", Backgrounded: true},
		{Status: "completed", Backgrounded: true},
		{Status: "failed", Backgrounded: true},
	}
	terminal, failed := historyCounts(agents, shells)
	if terminal != 4 || failed != 2 {
		t.Fatalf("historyCounts = %d, %d; want 4, 2", terminal, failed)
	}
}

func TestRootTranscriptToolsDoNotCreateHistory(t *testing.T) {
	m := newQueueTestModel()
	m.running = []runningTool{{name: "bash"}}
	for _, it := range m.activityItems() {
		if it.kind == testChipHistory {
			t.Fatalf("root tool created history: %+v", it)
		}
	}
}

// ctrl+g focuses the chip that needs attention; arrows move; enter opens its
// view; esc leaves; any other key leaves and reaches the composer.
func TestActivityFocusNavigation(t *testing.T) {
	m := newQueueTestModel()
	m.jobs = []agentJob{{ID: "a1", Type: "explore", Status: chat.AgentStatusRunning}}

	m = stripKeys(t, m, kt(tea.KeyCtrlG))
	if !m.activityFocus || m.activityItems()[m.activitySel].kind != chipAgent {
		t.Fatalf("focus=%v sel=%d, want the running agent selected", m.activityFocus, m.activitySel)
	}
	if vs := m.viewState(); vs.HelpRight != "" || !strings.Contains(vs.Help, "enter open") {
		t.Fatalf("focused help = %q / %q", vs.Help, vs.HelpRight)
	}

	m = stripKeys(t, m, kt(tea.KeyLeft))
	if m.activitySel != 0 {
		t.Fatalf("sel after left = %d, want 0", m.activitySel)
	}
	m = stripKeys(t, m, kt(tea.KeyEnter))
	if m.activityFocus || !m.showTodo {
		t.Fatalf("enter on the todo chip: focus=%v showTodo=%v", m.activityFocus, m.showTodo)
	}
	m = stripKeys(t, m, kt(tea.KeyEsc))

	m = stripKeys(t, m, kt(tea.KeyCtrlG), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	if m.activityFocus || m.textarea.Value() != "h" {
		t.Fatalf("typing while focused: focus=%v composer=%q, want the key in the composer", m.activityFocus, m.textarea.Value())
	}
}

// Enter on a running agent's chip opens that agent's log directly.
func TestEnterOnAgentOpensItsLog(t *testing.T) {
	m := newQueueTestModel()
	m.jobs = []agentJob{
		{ID: "a1", Type: "explore", Status: chat.AgentStatusRunning},
		{ID: "a2", Type: "plan", Status: chat.AgentStatusRunning},
	}
	m = stripKeys(t, m, kt(tea.KeyCtrlG), kt(tea.KeyRight), kt(tea.KeyEnter))
	if !m.showLogs || m.logOpenID != "a2" || m.logOpenKind != "agent" {
		t.Fatalf("showLogs=%v open=%q/%q, want a2's log", m.showLogs, m.logOpenID, m.logOpenKind)
	}
}

// The loops chip opens a panel listing each loop, and esc closes it.
func TestLoopsChipOpensPanel(t *testing.T) {
	m := newQueueTestModel()
	m.loops = loop.NewRegistry()
	m.loops.Add("*/10 * * * *", "check the render queue", true, false, loop.MonitorConfig{})
	items := m.activityItems()
	if items[len(items)-1].kind != chipLoops {
		t.Fatalf("strip = %v, want a loops chip", chipTexts(m))
	}
	m.activityFocus, m.activitySel = true, len(items)-1
	m = stripKeys(t, m, kt(tea.KeyEnter))
	if m.infoPanel != chipLoops || !strings.Contains(m.renderInfoPanel(), "check the render queue") {
		t.Fatalf("panel %q = %q", m.infoPanel, m.renderInfoPanel())
	}
	if m.footerRows() != nil {
		t.Fatal("the strip should hide while a panel owns the body")
	}
	m = stripKeys(t, m, kt(tea.KeyEsc))
	if m.infoPanel != "" {
		t.Fatal("esc did not close the panel")
	}
}

func TestTelemetryItems(t *testing.T) {
	cases := map[string][]string{
		"":                  {"context", "speed", "clock", "cpu", "mem"},
		"none":              nil,
		"clock, cpu,bogus":  {"clock", "cpu"},
		"usage age context": {"usage", "age", "context"},
	}
	for in, want := range cases {
		got := telemetryItems(in, defaultFooterFront)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("telemetryItems(%q) = %v, want %v", in, got, want)
		}
	}
}

// The machine telemetry is up front by default; the expanded line (session
// age here) shows only while the strip has focus; ui.footer_expanded moves an
// item there.
func TestExpandedTelemetryFollowsFocusAndConfig(t *testing.T) {
	m := withHUD(newQueueTestModel())
	m.width = 200
	m.sessionCreated = time.Now().Add(-time.Hour)
	if vs := m.viewState(); vs.Expanded != "" || !strings.Contains(vs.Badges, "cpu") || !strings.Contains(vs.Badges, "12:34:56") {
		t.Fatalf("unfocused: badges %q, expanded %q", vs.Badges, vs.Expanded)
	}
	m.activityFocus = true
	if vs := m.viewState(); !strings.Contains(vs.Expanded, "age 1h") {
		t.Fatalf("focused expanded = %q, want the session age", vs.Expanded)
	}
	m.cfg.UI.FooterFront, m.cfg.UI.FooterExpanded = "context", "clock,cpu"
	if vs := m.viewState(); strings.Contains(vs.Badges, "cpu") || !strings.Contains(vs.Expanded, "cpu") {
		t.Fatalf("configured: front %q, expanded %q; want cpu moved to the expanded line", vs.Badges, vs.Expanded)
	}
}

func TestHumanAge(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second:             "30s",
		12 * time.Minute:             "12m",
		5*time.Hour + 12*time.Minute: "5h 12m",
		7*24*time.Hour + 5*time.Hour: "7d 5h",
	} {
		if got := humanAge(d); got != want {
			t.Errorf("humanAge(%v) = %q, want %q", d, got, want)
		}
	}
}

// The ctrl+g hint survives a terminal too narrow to fit it on the help line.
func TestActivityHintSurvivesNarrowTerminal(t *testing.T) {
	m := newQueueTestModel()
	m.width = 70
	out := m.presenter.Footer(m.viewState(), m.width)
	if !strings.Contains(out, "ctrl+g activity") {
		t.Fatalf("footer at 70 cells lost the hint:\n%s", out)
	}
}

func TestHistoryUsesOnlyRetainedBackgroundShellRegistryJobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jobs := wizmcp.NewShellJobs()
	transports, err := wizmcp.StartTransports(ctx, types.Config{}, jobs, nil, nil)
	if err != nil {
		t.Fatalf("start transports: %v", err)
	}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "history-test", Version: "v0"}, nil)
	session, err := client.Connect(ctx, transports[0], nil)
	if err != nil {
		t.Fatalf("connect bash transport: %v", err)
	}
	defer session.Close()
	for _, call := range []*sdkmcp.CallToolParams{
		{Name: "bash", Arguments: map[string]any{"script": "true"}},
		{Name: "bash_background", Arguments: map[string]any{"script": "true"}},
	} {
		if _, err := session.CallTool(ctx, call); err != nil {
			t.Fatalf("call %s: %v", call.Name, err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		listed := jobs.List()
		if len(listed) == 2 && listed[0].Status != "running" && listed[1].Status != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	m := newQueueTestModel()
	m.shellJobs = jobs
	var history []activityItem
	for _, item := range m.activityItems() {
		if item.kind == "history" {
			history = append(history, item)
		}
	}
	if len(history) != 1 || history[0].row.Text != "History 1" {
		t.Fatalf("history = %+v, want only the completed retained background job", history)
	}
}
