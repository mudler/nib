package chat

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// A sub-agent can hang: a tool that never returns, a request that never
// finishes. The parent then waits on it with no sign that anything is wrong.
// While a turn runs, the session checks every staleAgentCheck for running
// sub-agents with no activity (start, tool call or tool result) for
// staleAgentAfter, and tells the model once per stall. New activity re-arms
// the notice; a finished agent is forgotten.
const (
	staleAgentAfter = 5 * time.Minute
	staleAgentCheck = 30 * time.Second
)

// touchLocked records activity for agentID at now. s.mu must be held.
func (s *agentLogStore) touchLocked(agentID string, now time.Time) {
	s.lastActive[agentID] = now
	delete(s.notified, agentID)
}

// started records that agentID began running.
func (s *agentLogStore) started(agentID string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchLocked(agentID, now)
}

// forget drops the stall tracking for an agent that finished.
func (s *agentLogStore) forget(agentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.lastActive, agentID)
	delete(s.notified, agentID)
}

// stale returns, sorted, the agents idle for longer than after at now whose
// stall is not reported yet, with how long each has been idle. Idleness
// counts from quietSince at the earliest: time spent waiting on the backend
// is not a stall.
func (s *agentLogStore) stale(now, quietSince time.Time, after time.Duration) ([]string, map[string]time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	idle := map[string]time.Duration{}
	for id, t := range s.lastActive {
		if quietSince.After(t) {
			t = quietSince
		}
		if d := now.Sub(t); d > after && !s.notified[id] {
			ids = append(ids, id)
			idle[id] = d
		}
	}
	sort.Strings(ids)
	return ids, idle
}

// markNotified records that the stall of agentID was reported.
func (s *agentLogStore) markNotified(agentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.lastActive[agentID]; ok {
		s.notified[agentID] = true
	}
}

// notifyStaleAgents tells the live run about each sub-agent that stalled at
// now. A notice the run could not take is retried on the next check.
func (s *Session) notifyStaleAgents(now time.Time) {
	if s.agentLogs == nil {
		return
	}
	ids, idle := s.agentLogs.stale(now, s.agentBackoff.quietSince(now), staleAgentAfter)
	for _, id := range ids {
		msg := fmt.Sprintf("Sub-agent %s shows no activity for %s. It may be stuck. "+
			"Inspect it with agent_logs or check_agent, then decide whether to keep waiting "+
			"or go on without it.", id, idle[id].Round(time.Second))
		if s.Inject(msg) {
			s.agentLogs.markNotified(id)
		}
	}
}

// A background shell job can stall the same way: no output and no exit. The
// check below uses the same tick as the sub-agent one. The limit is long,
// because a quiet build is normal; shellStallAfter on the session overrides it.
const defaultShellStallAfter = 10 * time.Minute

// notifyStalledShells tells the live run about each background shell job that
// stalled at now, once per stall. New output from a job re-arms its notice. A
// notice the run could not take is retried on the next check.
func (s *Session) notifyStalledShells(now time.Time) {
	if s.shellJobs == nil {
		return
	}
	after := s.shellStallAfter
	if after <= 0 {
		after = defaultShellStallAfter
	}
	for _, j := range s.shellJobs.Stalled(now, after) {
		msg := fmt.Sprintf("Shell job %s has printed nothing and has not exited for %s (command: %s). "+
			"It may be stuck. Read it with bash_job_output, then decide whether to keep waiting, "+
			"or stop it with bash_job_kill and go on without it.",
			j.ID, j.Idle.Round(time.Second), shortCommand(j.Script))
		if s.Inject(msg) {
			s.shellJobs.MarkStallReported(j.ID)
		}
	}
}

func shortCommand(script string) string {
	script = strings.Join(strings.Fields(script), " ")
	if len(script) > 80 {
		script = script[:80] + "..."
	}
	return script
}

// watchStaleAgents checks for stalled sub-agents and shell jobs until ctx is done.
func (s *Session) watchStaleAgents(ctx context.Context) {
	ticker := time.NewTicker(staleAgentCheck)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.notifyStaleAgents(now)
			s.notifyStalledShells(now)
		}
	}
}
