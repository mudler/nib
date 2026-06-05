package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/mudler/nib/types"

	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"
)

// compactInstruction is the prompt prefix used to summarize the older portion
// of a conversation during compaction.
const compactInstruction = "You are compacting a conversation to save context. " +
	"Summarize the conversation below, preserving decisions made, facts established, " +
	"file paths, identifiers, and any open tasks or unresolved questions. " +
	"Be concise but complete. Output only the summary."

// splitForCompaction partitions msgs into a head (to be summarized) and a tail
// (kept verbatim). keepRecent is the desired tail length; the boundary is moved
// backward so an assistant tool_calls message is never separated from its tool
// result messages (which would make the next API call invalid). It returns
// (nil, nil) when there is nothing worth compacting.
func splitForCompaction(msgs []openai.ChatCompletionMessage, keepRecent int) (head, tail []openai.ChatCompletionMessage) {
	if keepRecent < 1 {
		keepRecent = 1
	}
	start := len(msgs) - keepRecent
	if start < 1 {
		return nil, nil
	}
	// Never begin the tail on a tool result, and never leave a dangling
	// assistant tool_calls at the end of the head.
	for start > 0 && (msgs[start].Role == "tool" || len(msgs[start-1].ToolCalls) > 0) {
		start--
	}
	if start < 1 {
		return nil, nil
	}
	return msgs[:start], msgs[start:]
}

// shouldAutoCompact reports whether the last request's prompt tokens crossed the
// configured fraction of the context window. Auto-compaction is off when
// Disabled or when no context window is configured.
func shouldAutoCompact(cfg types.CompactionConfig, promptTokens int) bool {
	if cfg.Disabled || cfg.MaxContextTokens <= 0 {
		return false
	}
	threshold := cfg.Threshold
	if threshold <= 0 || threshold > 1 {
		threshold = 0.8
	}
	limit := int(float64(cfg.MaxContextTokens) * threshold)
	return promptTokens >= limit
}

// estimateTokens is a cheap byte/4 approximation, used when no real usage figure
// is available (e.g. right after a rebuild, before the next live turn). It only
// counts m.Content and tool calls, ignoring m.MultiContent (multimedia parts).
func estimateTokens(msgs []openai.ChatCompletionMessage) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
		for _, tc := range m.ToolCalls {
			n += len(tc.Function.Name) + len(tc.Function.Arguments)
		}
	}
	return n / 4
}

// ContextTokens reports the current conversation size in tokens for display:
// the last request's reported prompt tokens, or a byte/4 estimate when the
// backend hasn't reported usage yet (e.g. before the first turn). This is the
// same signal the auto-compaction trigger watches.
func (s *Session) ContextTokens() int {
	if s.fragment.Status != nil && s.fragment.Status.LastUsage.PromptTokens > 0 {
		return s.fragment.Status.LastUsage.PromptTokens
	}
	return estimateTokens(s.fragment.Messages)
}

// formatTokenCount returns the bare magnitude string for a token count, e.g.
// 950 → "950", 12000 → "12k", 47200 → "47.2k". A trailing ".0" is trimmed.
// Returns "" for zero/negative so callers can omit the segment.
func formatTokenCount(n int) string {
	if n <= 0 {
		return ""
	}
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	s := fmt.Sprintf("%.1f", float64(n)/1000.0)
	s = strings.TrimSuffix(s, ".0")
	return s + "k"
}

// HumanTokens formats a token count compactly (e.g. 47200 → "47.2k").
func HumanTokens(n int) string {
	return formatTokenCount(n)
}

// renderMessages flattens messages to plain "role: content" lines for the
// summarization prompt, skipping system boilerplate and rendering tool calls
// inline. A message carrying both content and tool calls renders both. Like
// estimateTokens, this ignores m.MultiContent (multimedia parts).
func renderMessages(msgs []openai.ChatCompletionMessage) string {
	var b strings.Builder
	for _, m := range msgs {
		if m.Role == "system" {
			continue
		}
		if m.Content != "" {
			fmt.Fprintf(&b, "%s: %s\n", m.Role, m.Content)
		}
		for _, tc := range m.ToolCalls {
			fmt.Fprintf(&b, "%s: [tool call %s(%s)]\n", m.Role, tc.Function.Name, tc.Function.Arguments)
		}
	}
	return b.String()
}

// CompactHistory summarizes the older portion of the conversation via the LLM
// and rebuilds the fragment as [summary] + recent tail, keeping the display
// copy consistent. It returns byte/4 token estimates of the conversation before
// and after compaction (before==after signals a no-op). On summary failure it
// returns the error WITHOUT mutating session state (atomic swap).
func (s *Session) CompactHistory() (before, after int, err error) {
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s.compactHistory(ctx)
}

// compactHistory is the context-aware implementation of CompactHistory. The
// passed-in ctx governs the summarization LLM call, allowing callers (e.g.
// auto-compaction) to make it cancellable via a per-turn context.
func (s *Session) compactHistory(ctx context.Context) (before, after int, err error) {
	msgs := s.fragment.Messages

	before = estimateTokens(msgs)

	head, tail := splitForCompaction(msgs, s.compaction.KeepRecent)
	headContent := renderMessages(head)
	if strings.TrimSpace(headContent) == "" {
		return before, before, nil // nothing to compact
	}

	prompt := compactInstruction + "\n\n--- CONVERSATION ---\n" + headContent
	res, aerr := s.llm.Ask(ctx, cogito.NewFragment().AddMessage(cogito.UserMessageRole, prompt))
	if aerr != nil {
		return before, before, fmt.Errorf("compaction summary failed: %w", aerr)
	}
	last := res.LastMessage()
	if last == nil || strings.TrimSpace(last.Content) == "" {
		return before, before, fmt.Errorf("compaction produced an empty summary")
	}

	// Build the new state up front; swap only after success (atomic).
	summaryMsg := openai.ChatCompletionMessage{
		Role:    "user",
		Content: "[Earlier conversation compacted]\n\n" + last.Content,
	}
	newFragMsgs := append([]openai.ChatCompletionMessage{summaryMsg}, tail...)

	displayTail := []openai.ChatCompletionMessage{}
	for _, m := range tail {
		if (m.Role == "user" || m.Role == "assistant") && strings.TrimSpace(m.Content) != "" {
			displayTail = append(displayTail, openai.ChatCompletionMessage{Role: m.Role, Content: m.Content})
		}
	}
	removed := len(s.messages) - len(displayTail)
	if removed < 0 {
		removed = 0
	}
	newMessages := append([]openai.ChatCompletionMessage{{
		Role:    "assistant",
		Content: fmt.Sprintf("📦 Compacted %d earlier messages", removed),
	}}, displayTail...)

	newFrag := cogito.NewFragment(newFragMsgs...)
	if s.fragment.Status != nil {
		newFrag.Status = s.fragment.Status // preserve running token counters
	}
	s.fragment = newFrag
	s.messages = newMessages

	after = estimateTokens(newFrag.Messages)
	return before, after, nil
}
