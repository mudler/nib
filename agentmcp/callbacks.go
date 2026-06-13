package agentmcp

import "github.com/mudler/nib/chat"

// buildCallbacks wires chat.Session lifecycle events into the reply router and
// approval policy. OnParked -> Pending reply (work continues); OnResponse ->
// final reply; OnError -> terminal error event.
func buildCallbacks(r *router, pol policy) chat.Callbacks {
	return chat.Callbacks{
		OnParked: func(reply string) {
			r.emit(replyEvent{Text: reply, Pending: true})
		},
		OnResponse: func(response string) {
			r.emit(replyEvent{Text: response})
		},
		OnError: func(err error) {
			r.emit(replyEvent{Err: err})
		},
		OnToolCall: pol.decide,
		OnAskUser: func(chat.AskRequest) string {
			return pol.askDefault
		},
	}
}
