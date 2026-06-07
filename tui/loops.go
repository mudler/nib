package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/loop"
	"github.com/mudler/nib/slash"
	"github.com/mudler/nib/theme"
)

// durationToCron maps a /loop interval to a 5-field cron expression. Intervals
// under a minute fire every minute (cron's finest granularity); hour-aligned
// intervals use the hour field; other minute intervals use the minute field,
// falling back to hourly when the minute step would exceed 59.
func durationToCron(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "* * * * *"
	case d%time.Hour == 0:
		h := int(d / time.Hour)
		return fmt.Sprintf("0 */%d * * *", h)
	default:
		m := int(d / time.Minute)
		if m > 59 {
			return "0 * * * *" // ≥1h non-aligned → hourly
		}
		return fmt.Sprintf("*/%d * * * *", m)
	}
}

// renderLoopsFooter renders a one-line summary of active cron loops, plus a
// self-paced indicator when selfPaced > 0. Returns "" when nothing is active.
func renderLoopsFooter(r *loop.Registry, selfPaced, width int) string {
	if r == nil {
		if selfPaced == 0 {
			return ""
		}
		return theme.Subtle.Render(fmt.Sprintf("%s %d loop(s): %d self-paced  (/loop list · /loop stop)", theme.Loop, selfPaced, selfPaced))
	}
	jobs := r.List()
	if len(jobs) == 0 && selfPaced == 0 {
		return ""
	}
	var parts []string
	for _, j := range jobs {
		parts = append(parts, fmt.Sprintf("%s %s%s%s", j.ID, j.Expr, theme.Arrow, truncateRunes(j.Prompt, 24)))
	}
	if selfPaced > 0 {
		parts = append(parts, fmt.Sprintf("%d self-paced", selfPaced))
	}
	line := fmt.Sprintf("%s %d loop(s): %s  (/loop list · /loop stop)", theme.Loop, len(jobs)+selfPaced, strings.Join(parts, " · "))
	return theme.Subtle.Render(line)
}

// dispatchLoop runs a due loop payload, routing by run-state: inject into a
// parked run, queue while a run is live or starting, or start a fresh turn when
// idle. Returns a Cmd to run (the fresh-turn send) or nil.
func (m *Model) dispatchLoop(payload string) tea.Cmd {
	action := slash.Resolve(payload, m.cfg.Commands, m.cfg.Skills, m.cfg.Agents)
	text := action.Text
	if action.Kind != slash.KindSend || text == "" {
		text = payload
	}
	// A run is in flight (or about to be: m.loading is set the instant we return
	// a send Cmd, before the goroutine flips RunLive). Treat both as "live" so
	// multiple jobs due in one tick don't launch concurrent turns.
	live := m.session != nil && (m.session.RunLive() || m.loading)
	switch {
	case live && m.parked:
		if m.session.Inject(text) {
			m.messages = append(m.messages, ChatMessage{Role: "user", Content: payload})
			m.parked = false
			m.loading = true
			m.interruptArmed = false
			m.status = "Thinking…"
			m.updateViewport()
		}
		return nil
	case live:
		// Run live or starting: queue; drains at the next boundary.
		m.queue = append(m.queue, payload)
		m.updateViewport()
		return nil
	case m.sessionReady && m.session != nil && !m.awaitingApproval && !m.awaitingAsk:
		m.messages = append(m.messages, ChatMessage{Role: "user", Content: payload})
		m.loading = true
		m.interruptArmed = false
		m.status = "Thinking…"
		m.updateViewport()
		return m.sendMessage(text)
	}
	return nil
}

// startLoop begins a fixed-interval or self-paced loop and returns a Cmd that
// runs the first iteration.
func (m *Model) startLoop(a slash.Action) tea.Cmd {
	// Validate the payload up front so a bad command fails loudly, not silently.
	pa := slash.Resolve(a.Payload, m.cfg.Commands, m.cfg.Skills, m.cfg.Agents)
	if pa.Kind == slash.KindError {
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: "loop payload: " + pa.Err})
		m.updateViewport()
		return nil
	}
	first := a.Payload
	if pa.Kind == slash.KindSend && pa.Text != "" {
		first = pa.Text
	}

	if a.Interval == 0 {
		// Self-paced: run once + inject the convention; the model re-arms.
		m.selfPaced++
		m.messages = append(m.messages, ChatMessage{Role: "user", Content: a.Payload})
		m.loading = true
		m.interruptArmed = false
		m.status = "Thinking…"
		m.updateViewport()
		return m.sendMessage(selfPacedPreamble(a.Payload) + first)
	}

	// Fixed interval: register a (non-durable) cron job + run the first now.
	expr := durationToCron(a.Interval)
	j, err := m.loops.Add(expr, a.Payload, true, false)
	if err != nil {
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: "loop: " + err.Error()})
		m.updateViewport()
		return nil
	}
	m.messages = append(m.messages, ChatMessage{Role: "agent", Content: fmt.Sprintf("Looping %q %s (%s). Stop with /loop stop %s.", a.Payload, a.Interval, j.ID, j.ID)})
	m.messages = append(m.messages, ChatMessage{Role: "user", Content: a.Payload})
	m.loading = true
	m.interruptArmed = false
	m.status = "Thinking…"
	m.updateViewport()
	return m.sendMessage(first)
}

// stopLoop cancels one cron loop (by id) or all loops + self-paced.
func (m *Model) stopLoop(id string) string {
	if id != "" {
		if m.loops.Delete(id) {
			_ = m.loops.Save(m.loopsPath)
			return "Stopped " + id
		}
		return "No such loop: " + id
	}
	// Stop everything: clear cron jobs and disarm self-paced re-arms.
	n := len(m.loops.List())
	for _, j := range m.loops.List() {
		m.loops.Delete(j.ID)
	}
	_ = m.loops.Save(m.loopsPath)
	sp := m.selfPaced
	// Only bump the wake-up generation when a self-paced loop is actually
	// active, so we don't invalidate an unrelated agent-armed wake-up tick.
	if sp > 0 {
		m.wakeupGen++
		m.selfPaced = 0
		if m.session != nil && m.session.RunLive() {
			m.session.Inject("The user stopped the loop. Do not schedule another wake-up; finish the current iteration and reply normally.")
		}
	}
	return fmt.Sprintf("Stopped %d loop(s).", n+sp)
}

// listLoops renders active loops for the user.
func (m *Model) listLoops() string {
	jobs := m.loops.List()
	if len(jobs) == 0 && m.selfPaced == 0 {
		return "No active loops."
	}
	var b strings.Builder
	for _, j := range jobs {
		fmt.Fprintf(&b, "%s · %s · %q\n", j.ID, j.Expr, j.Prompt)
	}
	if m.selfPaced > 0 {
		fmt.Fprintf(&b, "%d self-paced loop(s) (model-driven)\n", m.selfPaced)
	}
	return strings.TrimRight(b.String(), "\n")
}

// selfPacedPreamble is prepended to the first iteration of a self-paced loop to
// teach the model the re-arm convention.
func selfPacedPreamble(task string) string {
	return "You are in a self-paced loop. Your task each iteration is below. " +
		"After finishing, decide whether the work is complete. To run another " +
		"iteration, call schedule_wakeup with delay_seconds (choose based on what " +
		"you are waiting for) and prompt set to the exact task text. If the task " +
		"is complete, reply normally and do NOT call schedule_wakeup — the loop " +
		"ends. The user can stop the loop anytime with /loop stop.\n\nTASK:\n" +
		task + "\n\n--- begin first iteration ---\n"
}
