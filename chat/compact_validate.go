package chat

import (
	"errors"
	"fmt"

	openai "github.com/sashabaranov/go-openai"
)

// Sentinel errors returned by validateCompaction. They are exported so the
// overflow-recovery caller can switch on them with errors.Is.
var (
	ErrCompactionNoOp       = errors.New("compaction was a no-op")
	ErrCompactionGrew       = errors.New("compaction result is larger than the original")
	ErrCompactionOverBudget = errors.New("compaction result exceeds the context budget")
	ErrCompactionEmpty      = errors.New("compaction result is empty")
)

// validateCompaction gates a compaction result before it is sent. It returns
// the first failing check: empty result, growth, no-op, over budget, then
// broken tool-call pairing. Growth is checked before no-op, because a result
// that grew is never equal in size and the growth check must be reachable.
// budget is decided by the caller (the context budget, minus any
// non-compactable floor it knows about).
func validateCompaction(before, after []openai.ChatCompletionMessage, budget int) error {
	if len(after) == 0 {
		return ErrCompactionEmpty
	}
	was, now := estimateTokens(before), estimateTokens(after)
	if now > was {
		return fmt.Errorf("%w: %d > %d tokens", ErrCompactionGrew, now, was)
	}
	if now == was {
		return fmt.Errorf("%w: still %d tokens", ErrCompactionNoOp, now)
	}
	if now > budget {
		return fmt.Errorf("%w: %d > %d tokens", ErrCompactionOverBudget, now, budget)
	}
	return validateToolPairing(after)
}

// validateToolPairing reports a tool result whose ToolCallID has no earlier
// assistant tool call, and an assistant tool call with no tool result after
// it. Either one makes the backend reject the request.
func validateToolPairing(msgs []openai.ChatCompletionMessage) error {
	pending := map[string]bool{} // call ID -> seen a result yet
	for i, m := range msgs {
		for _, tc := range m.ToolCalls {
			pending[tc.ID] = false
		}
		if m.Role == openai.ChatMessageRoleTool {
			answered, ok := pending[m.ToolCallID]
			if !ok {
				return fmt.Errorf("tool result %q at message %d has no matching tool call", m.ToolCallID, i)
			}
			if !answered {
				pending[m.ToolCallID] = true
			}
		}
	}
	for i, m := range msgs {
		for _, tc := range m.ToolCalls {
			if !pending[tc.ID] {
				return fmt.Errorf("tool call %q at message %d has no tool result", tc.ID, i)
			}
		}
	}
	return nil
}
