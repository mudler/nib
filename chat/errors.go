package chat

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// contextOverflowMarkers are substrings different backends emit when a request
// exceeds the model's context window. The phrasing varies:
//   - llama.cpp / LocalAI: "request (9739 tokens) exceeds the available context size (8192 tokens)"
//   - OpenAI:              "This model's maximum context length is 8192 tokens. However, your messages resulted in 9739 tokens"
//   - vLLM:                "maximum context length is 8192 tokens"
var contextOverflowMarkers = []string{
	"exceeds the available context size",
	"maximum context length",
	"context length",
	"context window",
	"context size",
}

// tokenCountRe pulls token counts (e.g. "8192 tokens") out of a backend error
// so we can report how far over the limit the request was.
var tokenCountRe = regexp.MustCompile(`(\d{3,})\s*tokens`)

// FriendlyError wraps a noisy backend error with a short, actionable message
// while preserving the original via Unwrap (so errors.Is/As still work).
type FriendlyError struct {
	err error
	msg string
}

func (e *FriendlyError) Error() string { return e.msg }
func (e *FriendlyError) Unwrap() error { return e.err }

// humanizeError rewrites known, verbose backend failures into a concise,
// actionable message. Unrecognized errors are returned unchanged, so callers
// can wrap the result unconditionally.
func humanizeError(err error) error {
	if err == nil {
		return nil
	}
	low := strings.ToLower(err.Error())
	for _, marker := range contextOverflowMarkers {
		if strings.Contains(low, marker) {
			return &FriendlyError{err: err, msg: contextOverflowMessage(err.Error())}
		}
	}
	return err
}

// contextOverflowMessage builds the user-facing text for a context-window
// overflow, folding in the token counts when the backend reported them. The
// larger count is the request size and the smaller is the model's limit,
// regardless of the order the backend printed them.
func contextOverflowMessage(raw string) string {
	detail := ""
	if m := tokenCountRe.FindAllStringSubmatch(raw, -1); len(m) >= 2 {
		needs, _ := strconv.Atoi(m[0][1])
		allows, _ := strconv.Atoi(m[1][1])
		if allows > needs {
			needs, allows = allows, needs
		}
		detail = fmt.Sprintf(" (needs ~%d tokens, model allows %d)", needs, allows)
	}
	return "the request is larger than the model's context window" + detail +
		". Increase the backend's context size, or reduce the enabled tools/MCP servers and clear the conversation (\"clear\"), then retry."
}
