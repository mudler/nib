package chat

import (
	"sync"

	"github.com/mudler/cogito"
)

// SessionUsage is a snapshot of everything a session has spent: every LLM call
// it made, including sub-agents and compaction summaries, plus the number of
// user turns those calls served.
//
// The JSON tags are load-bearing: the usage.json written beside a trace is read
// by benchmark harnesses, so the field names are a contract, not decoration.
type SessionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	Turns            int `json:"turns"`
}

// sessionUsage accumulates SessionUsage behind its own lock.
//
// Its own lock, and not historyMu, on purpose: historyMu is held across history
// rebuilds and compaction swaps, and routing counter updates through it would
// couple the run path to those and invite lock-ordering problems for a few
// integer adds.
//
// cogito's Status.CumulativeUsage is per-RUN (see cogito/tools.go): cogito sets
// it once at the end of each ExecuteTools call, and the session reassigns its
// fragment every turn, so that field always holds the LAST turn's total and
// never the session's. Mirroring it — which was the obvious first move — would
// silently under-report every multi-turn session. This is the session total.
type sessionUsage struct {
	mu sync.Mutex
	u  SessionUsage
}

func (c *sessionUsage) add(u cogito.LLMUsage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.u.PromptTokens += u.PromptTokens
	c.u.CompletionTokens += u.CompletionTokens
	c.u.TotalTokens += u.TotalTokens
}

func (c *sessionUsage) turn() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.u.Turns++
}

func (c *sessionUsage) snapshot() SessionUsage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.u
}

// addUsage folds one LLM call's usage into the session total. Counters only
// ever grow: compaction reduces the context, never the spend.
func (s *Session) addUsage(u cogito.LLMUsage) { s.usage.add(u) }

// countTurn records one completed user exchange. Sub-agent and compaction calls
// add tokens but never turns — a turn is what the user sees, not what the
// backend was asked.
func (s *Session) countTurn() { s.usage.turn() }

// Usage returns what this session has spent so far. Safe to call from any
// goroutine, which the TUI does on every render.
func (s *Session) Usage() SessionUsage { return s.usage.snapshot() }
