package tui

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mudler/nib/chat"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
)

// A root-agent tool call shows in the live area below the transcript from the
// moment it starts (chat.Callbacks.OnToolStart) until its result arrives: a
// header whose mark pulses, the time it has run, and, for a shell command,
// the tail of its output so far. When the result arrives the running block
// goes away and the finished block joins the transcript as before, so the
// transcript keeps the order in which calls finished.

// runningTool is a started root-agent tool call with no result yet.
type runningTool struct {
	id, name, args string
	started        time.Time
	queued         bool
	// flipped inverts the ctrl+r fold state for this block alone (a click).
	flipped bool
}

// toolStartMsg carries a started tool call to the UI.
type toolStartMsg chat.ToolStart

// runningToolOutputLines caps how much of a running command's output a block
// keeps, even expanded: the tail is what is changing.
const runningToolOutputLines = 1000

// startTool records a call that just started.
func (m *Model) startTool(ts chat.ToolStart) {
	if !m.loading || m.interruptArmed {
		return
	}
	key := toolKey(ts.ID, ts.Name, ts.Arguments)
	if m.finishedTools[key] > 0 {
		if ts.ID == "" {
			m.finishedTools[key]--
		}
		return
	}
	if ts.ID != "" {
		for i, r := range m.running {
			if r.id == ts.ID {
				if r.queued {
					m.running[i].queued = false
					m.running[i].started = time.Now()
				}
				return
			}
		}
	}
	m.running = append(m.running, runningTool{id: ts.ID, name: ts.Name, args: ts.Arguments, started: time.Now()})
}

type toolIdentity struct{ id, name, args string }

func toolKey(id, name, args string) toolIdentity {
	if id != "" {
		return toolIdentity{id: id}
	}
	return toolIdentity{name: name, args: args}
}

// ID-less producers get exact name/argument FIFO matching, never name-only.
// An unmatched result consumes one later start, rather than banning all calls
// with those arguments. Identified completions remain tombstoned for the turn.
func (m *Model) finishTool(res chat.ToolResult) time.Duration {
	key := toolKey(res.ID, res.Name, res.Arguments)
	if m.finishedTools == nil {
		m.finishedTools = make(map[toolIdentity]int)
	}
	for i, r := range m.running {
		if toolKey(r.id, r.name, r.args) == key {
			var took time.Duration
			if !r.queued {
				took = time.Since(r.started)
			}
			m.running = append(m.running[:i:i], m.running[i+1:]...)
			if res.ID != "" {
				m.finishedTools[key] = 1
			}
			return took
		}
	}
	m.finishedTools[key]++
	return 0
}

func (m *Model) clearRunning() {
	m.running = nil
	m.finishedTools = nil
}

// One unbounded mailbox orders text, starts and results without blocking producers
// or dropping bursts. Only Update drains it; a wakeup never owns events, so
// response completion can flush results even if its wakeup is still in flight.
type toolEvent struct {
	gen       uint64
	reasoning *reasoningEvent
	text      *strings.Builder
	queued    *chat.ToolStart
	start     *chat.ToolStart
	result    *chat.ToolResult
	park      *parkEvent
}
type toolEventsMsg []toolEvent
type toolEventsReadyMsg struct{}
type toolEventQueue struct {
	mu            sync.Mutex
	events        []toolEvent
	ready         chan struct{}
	gen           uint64
	active        bool
	epoch         uint64
	parked        bool
	receipt       receiptState
	childReceipts map[string]childReceipt
	done          chan struct{} // closed when this lifecycle ends or is superseded
}

func newToolEventQueue() *toolEventQueue { return &toolEventQueue{ready: make(chan struct{}, 1)} }
func (q *toolEventQueue) begin() {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.active && q.done != nil {
		close(q.done)
	}
	q.done = make(chan struct{})
	q.events = nil // a superseded lifecycle cannot retain pending text or tools
	q.gen++
	q.epoch++
	q.parked = false
	q.receipt = receiptState{}
	q.active = true
}
func (q *toolEventQueue) end() {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.active && q.done != nil {
		close(q.done)
	}
	q.active = false
}
func (q *toolEventQueue) generation() uint64 { q.mu.Lock(); defer q.mu.Unlock(); return q.gen }
func (q *toolEventQueue) activeGeneration(gen uint64) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.active && q.gen == gen
}
func (q *toolEventQueue) push(e toolEvent) { q.pushFor(q.generation(), e) }
func (q *toolEventQueue) pushFor(gen uint64, e toolEvent) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if gen != q.gen || (!q.active && (e.queued != nil || e.start != nil || e.park != nil || e.reasoning != nil)) {
		return
	}
	e.gen = gen
	// Coalesce adjacent token chunks without crossing any semantic boundary.
	// A builder avoids quadratic copying when the UI is slower than the stream.
	if e.reasoning != nil && (e.reasoning.kind == reasoningEventDelta || e.reasoning.kind == reasoningEventContentDelta) && len(q.events) > 0 {
		last := &q.events[len(q.events)-1]
		if last.reasoning != nil && last.start == nil && last.reasoning.kind == e.reasoning.kind && last.reasoning.gen == e.reasoning.gen && last.gen == gen {
			if last.text == nil {
				last.text = new(strings.Builder)
				last.text.WriteString(last.reasoning.text)
			}
			last.text.WriteString(e.reasoning.text)
			return
		}
	}
	q.events = append(q.events, e)
	select {
	case q.ready <- struct{}{}:
	default:
	}
}
func (q *toolEventQueue) drain() []toolEvent {
	q.mu.Lock()
	defer q.mu.Unlock()
	events := q.events
	for i := range events {
		if events[i].text != nil {
			events[i].reasoning.text = events[i].text.String()
			events[i].text = nil
		}
	}
	q.events = nil
	return events
}
func (m Model) listenToolEvents() tea.Cmd {
	if m.toolEvents == nil {
		return nil
	}
	return func() tea.Msg {
		select {
		case <-m.toolEvents.ready:
			return toolEventsReadyMsg{}
		case <-m.ctx.Done():
			return nil
		}
	}
}

// toolCallbacks snapshots the lifecycle generation for one SendMessage.
func (m Model) toolCallbacks() (func(chat.ToolStart), func(chat.ToolResult)) {
	gen := m.toolEvents.generation()
	return func(ts chat.ToolStart) {
			if !m.toolEvents.activeGeneration(gen) {
				return
			}
			marker := reasoningEvent{kind: reasoningEventStepEnd, gen: m.currentTurnGen(), toolGen: gen}
			m.toolEvents.pushFor(gen, toolEvent{reasoning: &marker, start: &ts})
		}, func(res chat.ToolResult) {
			m.toolEvents.pushFor(gen, toolEvent{result: &res})
		}
}

func (m *Model) applyToolEvents(events []toolEvent) {
	for _, e := range events {
		if m.toolEvents != nil && e.gen != m.toolEvents.generation() {
			continue
		}
		if e.reasoning != nil {
			m.applyReasoningEvents(reasoningEventsMsg{*e.reasoning})
		}
		if e.park != nil && !m.interruptArmed {
			park := parkMsg(*e.park)
			park.mailboxOrdered = true
			next, _ := m.Update(park)
			*m = next.(Model)
		}
		if e.queued != nil {
			m.queueTool(*e.queued)
		}
		if e.start != nil {
			m.startTool(*e.start)
		}
		if e.result != nil {
			m.applyToolResult(*e.result)
		}
	}
	m.updateViewport()
}

// toolsExpanded is the ctrl+r fold state that tool blocks share with the
// thinking: expanded when the reasoning is.
func (m Model) toolsExpanded() bool {
	return !m.reasoningCollapsed
}

// runningBlock builds the render data for one running call.
func (m Model) runningBlock(r runningTool) render.RunningTool {
	rt := render.RunningTool{
		Label:    toolLabel(r.name, r.args),
		Elapsed:  time.Since(r.started),
		Expanded: m.toolsExpanded() != r.flipped,
	}
	if r.queued {
		rt.Queued = true
		rt.Elapsed = 0
		return rt
	}
	if r.name == "bash" {
		if id, ok := m.foregroundShellJob(r.args); ok {
			rt.Hint = theme.ToolBackgroundHint
			rt.Output = m.shellOutput(id)
		}
	}
	return rt
}

// foregroundShellJob finds the running foreground shell job a bash call
// started. That job is what ctrl+b backgrounds.
func (m Model) foregroundShellJob(args string) (string, bool) {
	return findForegroundJob(m.shellJobs.List(), args)
}

// findForegroundJob returns the newest running, not backgrounded job whose
// script is the bash call's (args is the call's JSON arguments).
func findForegroundJob(jobs []wizmcp.ShellJobInfo, args string) (string, bool) {
	var a struct {
		Script string `json:"script"`
	}
	if json.Unmarshal([]byte(args), &a) != nil {
		return "", false
	}
	for i := len(jobs) - 1; i >= 0; i-- {
		j := jobs[i]
		if j.Running && !j.Backgrounded && j.Script == a.Script {
			return j.ID, true
		}
	}
	return "", false
}

// shellOutput returns a job's output so far (see joinShellOutput).
func (m Model) shellOutput(id string) string {
	stdout, stderr, ok := m.shellJobs.Output(id)
	if !ok {
		return ""
	}
	return joinShellOutput(stdout, stderr)
}

// joinShellOutput puts stderr after stdout and keeps the last
// runningToolOutputLines lines.
func joinShellOutput(stdout, stderr string) string {
	out := strings.TrimRight(stdout, "\n")
	if e := strings.TrimRight(stderr, "\n"); e != "" {
		if out != "" {
			out += "\n"
		}
		out += e
	}
	lines := strings.Split(out, "\n")
	if len(lines) > runningToolOutputLines {
		lines = lines[len(lines)-runningToolOutputLines:]
	}
	return strings.Join(lines, "\n")
}

// toolSpan is the content-relative row span [start, end) one tool block takes
// in the viewport this render, and which block it is: an index into
// m.messages, or into m.running when running is set.
type toolSpan struct {
	start, end int
	index      int
	running    bool
}

// toolBlockHit returns the tool block a terminal-relative mouse Y lands in.
// The translation from Y to a content row is reasoningBoxHit's.
func (m Model) toolBlockHit(y int) (toolSpan, bool) {
	if !m.showingViewport() {
		return toolSpan{}, false
	}
	row := y - m.presenter.HeaderHeight(m.viewState()) + m.viewport.YOffset
	for _, s := range m.toolSpans {
		if row >= s.start && row < s.end {
			return s, true
		}
	}
	return toolSpan{}, false
}

// toggleToolBlock flips the fold of the block s names.
func (m *Model) toggleToolBlock(s toolSpan) {
	if s.running {
		if s.index < len(m.running) {
			m.running[s.index].flipped = !m.running[s.index].flipped
		}
		return
	}
	if s.index < len(m.messages) && m.messages[s.index].Role == "tool" {
		m.messages[s.index].flipped = !m.messages[s.index].flipped
	}
}

// clearToolFlips drops every per-block fold choice, so the blocks follow
// ctrl+r together again.
func (m *Model) clearToolFlips() {
	for i := range m.messages {
		m.messages[i].flipped = false
	}
	for i := range m.running {
		m.running[i].flipped = false
	}
}

// queueTool keeps waiting calls visible without starting execution duration.
func (m *Model) queueTool(ts chat.ToolStart) {
	if !m.loading || m.interruptArmed || m.finishedTools[toolKey(ts.ID, ts.Name, ts.Arguments)] > 0 {
		return
	}
	for _, r := range m.running {
		if toolKey(r.id, r.name, r.args) == toolKey(ts.ID, ts.Name, ts.Arguments) {
			return
		}
	}
	m.running = append(m.running, runningTool{id: ts.ID, name: ts.Name, args: ts.Arguments, queued: true})
}
func (m Model) queuedToolCallback() func(chat.ToolStart) {
	gen := m.toolEvents.generation()
	return func(ts chat.ToolStart) {
		marker := reasoningEvent{kind: reasoningEventStepEnd, gen: m.currentTurnGen(), toolGen: gen}
		m.toolEvents.pushFor(gen, toolEvent{reasoning: &marker, queued: &ts})
	}
}
