package chat

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mudler/cogito"
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
	// cogito's sentinels. The turn builds these messages with the figures
	// only it knows; this is the fallback for an error it did not rewrite.
	if _, done := err.(*FriendlyError); !done {
		var te *cogito.ToolArgumentsTruncatedError
		switch {
		case errors.Is(err, cogito.ErrStreamInterrupted):
			return &FriendlyError{err: err, msg: streamInterruptedMessage(0)}
		case errors.Is(err, cogito.ErrToolArgumentsInvalid):
			return &FriendlyError{err: err, msg: toolArgsInvalidMessage}
		case errors.As(err, &te):
			return &FriendlyError{err: err, msg: toolArgsTruncatedMessage(te, truncCap)}
		}
	}
	return err
}

// streamInterruptedMessage is the text for a turn whose stream kept ending
// before the backend finished it. tries is how many times the turn was sent,
// or 0 when unknown.
func streamInterruptedMessage(tries int) string {
	n := ""
	if tries > 0 {
		n = fmt.Sprintf(" (tried %d times)", tries)
	}
	return "the connection to the model ended before the reply finished" + n +
		". The backend or a proxy in front of it may have timed out on a long reply."
}

const toolArgsInvalidMessage = "the model produced a tool call with invalid arguments several times. " +
	"Try rephrasing, or ask it to split the change into smaller steps."

// truncationCause is why a tool call was cut by finish_reason=length.
type truncationCause int

const (
	// truncCap: the output cap was reached with room left in the window.
	truncCap truncationCause = iota
	// truncToolCall: the window ran out while the model wrote a long call.
	truncToolCall
	// truncReasoning: the window ran out while the model was reasoning.
	truncReasoning
)

// toolArgsTruncatedMessage is the text for a tool call cut by the output
// limit, by cause.
func toolArgsTruncatedMessage(te *cogito.ToolArgumentsTruncatedError, cause truncationCause) string {
	switch cause {
	case truncToolCall:
		return fmt.Sprintf("the model's call to %s did not fit in the context window (about %d tokens of arguments on a prompt of %d). "+
			"Ask it to make the change in smaller steps.", te.ToolName, te.ArgumentsBytes/4, te.PromptTokens)
	case truncReasoning:
		return fmt.Sprintf("the model's reasoning filled the context window (about %d tokens on a prompt of %d). "+
			"Ask it to split the task, or lower the reasoning effort.", te.ReasoningBytes/4, te.PromptTokens)
	}
	limit := "the output limit"
	if te.MaxTokens > 0 {
		limit = fmt.Sprintf("the output limit (max_tokens %d)", te.MaxTokens)
	}
	return "the model's tool call was longer than " + limit +
		". Raise the model's max_tokens, or ask for smaller edits."
}

// truncationNote is the one-turn user-role note sent with the retry after a
// tool call was cut because the window ran out. It is never stored in the
// history.
func truncationNote(te *cogito.ToolArgumentsTruncatedError, cause truncationCause) string {
	if cause == truncReasoning {
		return fmt.Sprintf("Your previous reply was cut off: its reasoning (about %d tokens) ran out of room in the context window before the call to %s was complete. "+
			"Keep the plan shorter and work in smaller steps (for example write a file in parts, or use edit for targeted changes).",
			te.ReasoningBytes/4, te.ToolName)
	}
	return fmt.Sprintf("Your previous call to %s was cut off after about %d tokens because the reply ran out of room in the context window. "+
		"Split it into smaller calls (for example write the file in parts, or use edit for targeted changes).",
		te.ToolName, te.ArgumentsBytes/4)
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

// errSchemaFloor marks a turn that failed because the tool schemas and the
// system prompt alone leave no room for a request: no compaction can help, so
// nib kept the conversation instead of compacting it away.
var errSchemaFloor = errors.New("tool schemas and system prompt do not fit the context window")

// schemaFloorMessage is the user-facing text for errSchemaFloor. It reads
// like the schema-budget notice (sizes, the largest servers, what to do), and
// says the conversation was kept.
func schemaFloorMessage(sb SchemaBudget, window int) string {
	return schemaBudgetNotice(sb, window) +
		", or increase the backend's context size. The conversation was kept: compacting it cannot make the request fit."
}
