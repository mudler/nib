package chat

import "github.com/mudler/cogito"

// interruptedTurnNote closes a turn the user interrupted. Without it the model
// reads the user's message with no reply and may assume it answered.
const interruptedTurnNote = "[The user interrupted this request before it finished.]"

// failedTurnNote closes a turn that ended on an error.
func failedTurnNote(err error) string {
	return "[This request failed before it finished: " + err.Error() + "]"
}

// keepFailedTurn is the history a turn that did not finish leaves behind.
// committed is the session fragment the run started from; it already holds the
// user's message. returned is what cogito had built when it stopped: the tool
// steps it finished and the follow-ups it consumed.
//
// The finished steps are kept, but not an assistant message whose tool calls
// never got results: the API rejects that history. Status comes from
// committed, because the run's own Status holds its PastActions and would trip
// loop detection on the next turn.
func keepFailedTurn(committed, returned cogito.Fragment, note string) cogito.Fragment {
	kept := committed
	msgs := returned.Messages
	if n := len(msgs); n > 0 && msgs[n-1].Role == "assistant" && len(msgs[n-1].ToolCalls) > 0 {
		msgs = msgs[:n-1]
	}
	if len(msgs) > len(committed.Messages) {
		kept = replaceMessages(kept, msgs)
	}
	return kept.AddMessage("user", note)
}
