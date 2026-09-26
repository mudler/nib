package chat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/cogito"
	"github.com/mudler/nib/types"
	openai "github.com/sashabaranov/go-openai"
)

// A floor above the window: compaction cannot make any request fit, so an
// overflow must not summarize, must not touch the history, and must say which
// MCP servers take the room.
func TestOverflowFloorKeepsConversation(t *testing.T) {
	llm := &overflowLLM{failures: 99}
	s := newOverflowSession(t, llm)
	// The overflow teaches a 262144-token window; github alone is about 300k.
	s.clients = []*sdkmcp.ClientSession{
		connectTestMCP(t, "github", 60, 20000),
		connectTestMCP(t, "jira", 10, 4000),
		connectTestMCP(t, "linear", 4, 2000),
		connectTestMCP(t, "slack", 1, 100),
	}
	s.ensureSystemPrompt()
	before := append([]openai.ChatCompletionMessage(nil), s.fragment.Messages...)

	_, err := s.SendMessage("what changed?")
	if !errors.Is(err, errSchemaFloor) {
		t.Fatalf("err = %v, want the schema-floor error", err)
	}
	msg := err.Error()
	for _, want := range []string{"github", "jira", "linear", "kept"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q does not mention %q", msg, want)
		}
	}
	if strings.Contains(msg, "slack") {
		t.Fatalf("message %q names more than the three largest servers", msg)
	}
	llm.mu.Lock()
	asks := llm.asks
	llm.mu.Unlock()
	if asks != 0 {
		t.Fatalf("the summarizer ran %d times; the floor is the problem, not the history", asks)
	}
	if s.overflowRetries() != 0 {
		t.Fatalf("overflow retries = %d, want 0", s.overflowRetries())
	}
	got := s.fragment.Messages
	if len(got) != len(before) {
		t.Fatalf("fragment has %d messages, want the %d from before the turn", len(got), len(before))
	}
	for i := range got {
		if got[i].Role != before[i].Role || got[i].Content != before[i].Content {
			t.Fatalf("message %d changed: %+v", i, got[i])
		}
	}
	if n := len(s.messages); n == 0 || s.messages[n-1].Role != "user" || s.messages[n-1].Content != "what changed?" {
		t.Fatalf("the user's message is not the last in the display copy: %+v", s.messages)
	}
}

// A floor well below the window: recovery runs as before.
func TestOverflowFloorBelowWindowRecovers(t *testing.T) {
	llm := &overflowLLM{failures: 1}
	s := newOverflowSession(t, llm)
	if _, err := s.SendMessage("what changed?"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	llm.mu.Lock()
	asks := llm.asks
	llm.mu.Unlock()
	if asks == 0 || s.overflowRetries() != 1 {
		t.Fatalf("asks = %d, retries = %d: recovery did not run", asks, s.overflowRetries())
	}
}

// An estimate may undercount, so it blocks only when it alone is over the
// limit; a measured floor blocks once it leaves no room.
func TestOverflowFloorEstimateOnly(t *testing.T) {
	s := newCompactTestSession(&fakeSummaryLLM{}, 2, nil, nil) // window 1000
	s.systemPrompt = strings.Repeat("s", 3200)                 // ~800 tokens

	sb, blocked := s.schemaFloorBlocks(1000, 300)
	if sb.Measured || blocked {
		t.Fatalf("estimate %d below the window blocked (measured %v)", sb.Floor, sb.Measured)
	}
	s.live.recordFloor(sb.Floor)
	if _, blocked := s.schemaFloorBlocks(1000, 300); !blocked {
		t.Fatal("a measured floor that leaves no room did not block")
	}

	s2 := newCompactTestSession(&fakeSummaryLLM{}, 2, nil, nil)
	s2.systemPrompt = strings.Repeat("s", 4400) // ~1100 tokens
	if sb, blocked := s2.schemaFloorBlocks(1000, 0); !blocked {
		t.Fatalf("estimate %d over the window did not block", sb.Floor)
	}
}

// floorUsageLLM answers every turn request and reports its byte/4 size plus
// extra as prompt tokens, standing in for a large tool-schema floor.
type floorUsageLLM struct {
	mu    sync.Mutex
	extra int
	asks  int
}

func (f *floorUsageLLM) summary() string {
	f.mu.Lock()
	f.asks++
	f.mu.Unlock()
	return "summary of earlier turns"
}

func (f *floorUsageLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	if n := len(req.Messages); n > 0 && strings.HasPrefix(req.Messages[n-1].Content, compactInstruction) {
		return replyWith(f.summary()), cogito.LLMUsage{PromptTokens: 20, TotalTokens: 22}, nil
	}
	p := estimateTokens(req.Messages) + f.extra
	return replyWith("ok"), cogito.LLMUsage{PromptTokens: p, CompletionTokens: 1, TotalTokens: p + 1}, nil
}

func (f *floorUsageLLM) Ask(ctx context.Context, fr cogito.Fragment) (cogito.Fragment, error) {
	out := fr.AddMessage("assistant", f.summary())
	if out.Status != nil {
		out.Status.LastUsage = cogito.LLMUsage{PromptTokens: 20, TotalTokens: 22}
	}
	return out, nil
}

// End-of-turn auto-compaction is skipped when the floor alone is over the
// budget, and runs otherwise.
func TestOverflowFloorEndOfTurnCompaction(t *testing.T) {
	run := func(t *testing.T, extra int, sys, history string) (*floorUsageLLM, *Session) {
		llm := &floorUsageLLM{extra: extra}
		s := newOverflowSession(t, llm)
		if sys != "" {
			s.systemPrompt = sys
		}
		// Budget 40000 - 4096 = 35904; trigger 28723.
		s.compaction.MaxContextTokens = 40000
		s.fragment = cogito.NewFragment(
			openai.ChatCompletionMessage{Role: "user", Content: "u1"},
			openai.ChatCompletionMessage{Role: "assistant", Content: "a1 " + history},
			openai.ChatCompletionMessage{Role: "user", Content: "u2"},
			openai.ChatCompletionMessage{Role: "assistant", Content: "a2 " + history},
		)
		if _, err := s.SendMessage("next"); err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
		return llm, s
	}

	t.Run("floor over budget", func(t *testing.T) {
		// A system prompt of 37000 tokens: the floor is the fixed overhead
		// itself, not a skew the backend reports on top of the history.
		llm, s := run(t, 0, strings.Repeat("s", 37000*4), "short")
		if llm.asks != 0 {
			t.Fatalf("auto-compaction summarized %d times with the floor (%d) over the budget", llm.asks, s.SchemaBudget().Floor)
		}
		if !fragmentHas(s, func(m openai.ChatCompletionMessage) bool { return m.Content == "u1" }) {
			t.Fatal("the history was compacted away")
		}
	})
	t.Run("floor under budget", func(t *testing.T) {
		llm, _ := run(t, 5000, "", strings.Repeat("earlier detail ", 3500)) // ~13k tokens each
		if llm.asks == 0 {
			t.Fatal("auto-compaction did not run")
		}
	})
}

// Mid-turn compaction is skipped when the floor alone is over the budget, and
// runs otherwise.
func TestOverflowFloorMidTurnCompaction(t *testing.T) {
	msgs := func() []openai.ChatCompletionMessage {
		long := strings.Repeat("x", 400) // 100 tokens
		return []openai.ChatCompletionMessage{
			{Role: "user", Content: "u1 " + long}, {Role: "assistant", Content: "a1 " + long},
			{Role: "user", Content: "u2 " + long}, {Role: "assistant", Content: "a2 " + long},
			{Role: "user", Content: "u3 " + long}, {Role: "assistant", Content: "a3 " + long},
		}
	}
	for _, tc := range []struct {
		name    string
		floor   int
		compact bool
	}{
		{"floor over budget", 600, false}, // window 1000, budget 500
		{"floor under budget", 10, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			llm := &fakeSummaryLLM{reply: "S"}
			s := newCompactTestSession(llm, 2, nil, nil)
			s.live.recordFloor(tc.floor)
			in := msgs()
			out := s.newTurnCompactor(context.Background()).manipulate(in)
			if got := llm.calls > 0; got != tc.compact {
				t.Fatalf("summarized = %v, want %v", got, tc.compact)
			}
			if !tc.compact && len(out) != len(in) {
				t.Fatalf("request changed: %d messages, want %d", len(out), len(in))
			}
		})
	}
}

// With a large reserve (budget window/2) and a backend counting 1.5x, a long
// history must not read as a schema floor: the history is the problem, so
// end-of-turn auto-compaction runs. The old calibration took the whole
// residual (schemas plus the skew on the history) as the floor and skipped it.
func TestOverflowFloorSkewDoesNotBlockAutoCompaction(t *testing.T) {
	s := newCompactTestSession(&fakeSummaryLLM{}, 2, nil, nil)
	s.compaction = types.CompactionConfig{MaxContextTokens: 96000, ReserveTokens: 48000, Threshold: 0.8, KeepRecent: 2}
	llm := trackUsage(&skewLLM{ratio: 1.5}, &s.live, s.requestLimits)
	req, raw := skewRequest(5000, 90000)
	_, usage, err := llm.CreateChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !s.shouldCompactNow(usage.PromptTokens) {
		t.Fatalf("%d prompt tokens did not cross the trigger", usage.PromptTokens)
	}
	if s.autoCompactBlocked(10) {
		t.Fatalf("auto-compaction blocked by a floor of %d (schemas about %d x 1.5) with budget 48000",
			s.SchemaBudget().Floor, raw)
	}
}

// A tools-heavy request, schemas 80k of a 96k window and no skew, still
// blocks: there the schemas are the problem, whatever is compacted.
func TestOverflowFloorToolsHeavyBlocks(t *testing.T) {
	s := newCompactTestSession(&fakeSummaryLLM{}, 2, nil, nil)
	s.compaction = types.CompactionConfig{MaxContextTokens: 96000, ReserveTokens: 48000, Threshold: 0.8, KeepRecent: 2}
	llm := trackUsage(&skewLLM{ratio: 1}, &s.live, s.requestLimits)
	req, raw := skewRequest(80000, 1000)
	if _, _, err := llm.CreateChatCompletion(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	sb, blocked := s.schemaFloorBlocks(ContextBudget(s.compactionConfig(), s.contextWindow()), 10)
	if !blocked || !sb.Measured || sb.Floor != raw {
		t.Fatalf("floor %d (measured %v, want %d) did not block", sb.Floor, sb.Measured, raw)
	}
	if !s.autoCompactBlocked(10) {
		t.Fatal("auto-compaction not blocked by an 80k floor")
	}
}
