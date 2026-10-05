package chat

import (
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
)

// goalRepromptGuard is ephemeral session state protected by runMu. Only the
// stop gate records timestamps, never provider attempts or supervisor reviews.
type goalRepromptGuard struct {
	max        int
	window     time.Duration
	timestamps []time.Time
}

// allow commits one reminder decision. Ages equal to the window still count.
func (g *goalRepromptGuard) allow(now time.Time) bool {
	if g.max == -1 {
		return true
	}
	retained := g.timestamps[:0]
	for _, at := range g.timestamps {
		if now.Sub(at) <= g.window {
			retained = append(retained, at)
		}
	}
	g.timestamps = retained
	if len(g.timestamps) >= g.max {
		return false
	}
	g.timestamps = append(g.timestamps, now)
	return true
}

// AcceptHumanMessage resets the reminder budget at acceptance, including when
// a host queues input. Call once for nonempty human conversation text, not on
// redispatch, automatic input, approvals, or ask_user answers. It never resumes
// a paused goal. Acceptance and the reminder decision share runMu.
func (s *Session) AcceptHumanMessage(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	s.runMu.Lock()
	s.goalReprompts.timestamps = nil
	s.runMu.Unlock()
}

// appendGoalReminder commits the timestamp and history together against human
// acceptance. The caller has already checked cancellation and the terminal
// barrier. Lifecycle methods and callbacks run outside runMu.
func (s *Session) appendGoalReminder(now time.Time) bool {
	// Match turn setup: historyMu precedes runMu. Keep both until the
	// reminder and timestamp are committed, then release before callbacks.
	s.historyMu.Lock()
	s.runMu.Lock()
	goal := s.activeGoalLocked()
	if goal == "" || s.goalDone {
		s.runMu.Unlock()
		s.historyMu.Unlock()
		return false
	}
	if !s.goalReprompts.allow(now) {
		s.goalPaused = true
		notice := GoalPausedNotice{MaxReprompts: s.goalReprompts.max, Window: s.goalReprompts.window, Paused: true}
		s.runMu.Unlock()
		s.historyMu.Unlock()
		if s.goalSupervisor != nil {
			s.goalSupervisor.goalPause()
		}
		if s.callbacks.OnGoalPaused != nil {
			s.callbacks.OnGoalPaused(notice)
		}
		return false
	}
	reminder := goalReminder(goal)
	s.fragment = s.fragment.AddMessage("user", reminder)
	s.messages = append(s.messages, openai.ChatCompletionMessage{Role: "user", Content: reminder})
	s.runMu.Unlock()
	s.historyMu.Unlock()
	return true
}
