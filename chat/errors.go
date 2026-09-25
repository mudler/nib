package chat

import (
	"errors"
	"fmt"
	"strings"
)

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
	if isWindowOverflow(err) {
		return &FriendlyError{err: err, msg: contextOverflowMessage(err.Error())}
	}
	if isRateLimitError(err) {
		return &FriendlyError{err: err, msg: rateLimitMessage(err)}
	}
	if isEmptyReply(err) {
		return &FriendlyError{err: err, msg: emptyReplyMessage}
	}
	return err
}

// emptyReplyMarkers are the texts cogito uses when every decision attempt came
// back with no text and no tool call: "streaming decision produced no content
// (finish_reason=...)" on the streaming path, "no choices: 0" on the other.
//
// A backend that reports its failure (a context overflow, a crash) is matched
// before this by its own message. What is left is a backend that ended the
// reply without saying why, so the message can only list the likely causes.
var emptyReplyMarkers = []string{
	"produced no content",
	"no choices: 0",
}

func isEmptyReply(err error) bool {
	msg := err.Error()
	for _, marker := range emptyReplyMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

const emptyReplyMessage = "the model returned an empty reply (no text and no tool call) several times. " +
	"The backend may have dropped the request: check its log. " +
	"If the conversation is long, it may no longer fit the model's context: run /compact, then retry."

// humanizeTurnError is humanizeError plus the one fact only the turn knows:
// whether nib already compacted the conversation and re-sent it. An overflow
// that survives that must not advise the user to clear the conversation, which
// is the thing nib just did on their behalf.
//
// A second message rather than a flag threaded through contextOverflowMessage:
// the two texts differ in their subject as well as their advice ("is larger"
// versus "is STILL larger"), the first is reached from humanizeError on every
// non-turn path, and the shared half is already factored into overflowDetail.
// A bool parameter would leave one function whose every caller passes a
// constant, and both branches would still have to be tested separately.
func humanizeTurnError(err error, compacted bool) error {
	if compacted && isWindowOverflow(err) {
		return &FriendlyError{err: err, msg: contextOverflowRetriedMessage(err.Error())}
	}
	return humanizeError(err)
}

// isContextOverflow reports whether err is a backend complaining that the
// prompt itself did not fit the model's context window: the one rejection
// compaction can fix. A budget overflow (prompt + requested output) and an
// output-cap error are not context overflows and must not trigger compaction.
// Every overflow question in the package reads classifyOverflow, so the
// recovery path and the message path cannot drift apart.
func isContextOverflow(err error) bool {
	return classifyOverflow(err).Kind == KindContext
}

// isWindowOverflow reports a context or a budget overflow: the request, as
// sent, did not fit the window. It decides the user-facing message, which is
// true for both; recovery must still tell them apart.
func isWindowOverflow(err error) bool {
	k := classifyOverflow(err).Kind
	return k == KindContext || k == KindBudget
}

// learnedWindowFrom extracts the model's real context window from an overflow
// error, which is the one moment a backend reliably states it. The OpenAI
// /v1/models schema carries no context length and backends that expose one do
// so inconsistently, so this error is nib's only trustworthy source; inferring
// a window from a model name would be a guess presented as a fact.
//
// A window is learned only from a catalog row that names it (the "window"
// group): a generic wording, or a row that states only a request size, teaches
// nothing, because mistaking a request size for the window would raise the
// compaction trigger above the real limit. Context and budget overflows both
// state the real window; an output-cap error states the maximum OUTPUT, which
// is not the window.
//
// classifyOverflow walks the whole unwrap chain, so a humanized error (whose
// own text drops the figures) still teaches the window of the error it wraps.
func learnedWindowFrom(err error) (int, bool) {
	info := classifyOverflow(err)
	if (info.Kind == KindContext || info.Kind == KindBudget) && info.Window > 0 {
		return info.Window, true
	}
	return 0, false
}

// overflowNeeds is the request size an overflow states: the total, else input
// plus output, else the input alone (llama.cpp and OpenAI state only the
// prompt, and reserve no output in the count).
func (o overflowInfo) overflowNeeds() int {
	switch {
	case o.Total > 0:
		return o.Total
	case o.Input > 0 && o.Output > 0:
		return o.Input + o.Output
	default:
		return o.Input
	}
}

// overflowDetail renders the parenthesised token figures for an overflow
// message, or "" when the backend stated none. Which figure is the request
// size and which the limit comes from the catalog row, not number order.
func overflowDetail(raw string) string {
	info := classifyOverflow(errors.New(raw))
	needs := info.overflowNeeds()
	switch {
	case needs > 0 && info.Window > 0:
		return fmt.Sprintf(" (needs ~%d tokens, model allows %d)", needs, info.Window)
	case info.Window > 0:
		return fmt.Sprintf(" (model allows %d)", info.Window)
	case needs > 0:
		return fmt.Sprintf(" (needs ~%d tokens)", needs)
	}
	return ""
}

// contextOverflowMessage builds the user-facing text for the FIRST context-
// window overflow of a turn, folding in the token counts when the backend
// reported them. Clearing the conversation is sound advice here: nothing has
// been done about the size yet.
func contextOverflowMessage(raw string) string {
	return "the request is larger than the model's context window" + overflowDetail(raw) +
		". Increase the backend's context size, or reduce the enabled tools/MCP servers and clear the conversation (\"clear\"), then retry."
}

// contextOverflowRetriedMessage is the same overflow reported after nib already
// compacted the conversation and re-sent the turn, and it still did not fit.
//
// It says so, and it drops the "clear the conversation, then retry" advice the
// first message gives. That advice is actively wrong here: compaction just
// replaced the old turns with a summary and the retry already happened, so a
// user who followed it would spend their history discovering that nib had
// beaten them to it. What is left over is the request's fixed floor — the
// system prompt and the tool schemas — which only a bigger window or fewer
// tools can move.
func contextOverflowRetriedMessage(raw string) string {
	return "the request is still larger than the model's context window" + overflowDetail(raw) +
		" after compacting the conversation and retrying. Compacting again will not help: increase the backend's context size, or reduce the enabled tools/MCP servers."
}
