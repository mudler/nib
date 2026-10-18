// Package fragmentreply appends a legacy adapter's reply to a cogito fragment.
package fragmentreply

import (
	"github.com/mudler/cogito"
	"github.com/sashabaranov/go-openai"
)

// Append returns f with msg added at the end of its history.
//
// A fragment with authoritative history (built with WithConversation) must be
// extended through AppendMessages. Writing Messages alone leaves the two views
// out of step, and cogito then fails with ErrConversationProjectionMismatch.
// A legacy fragment has no authoritative history and keeps the plain Messages
// append, so it stays legacy.
func Append(f cogito.Fragment, msg openai.ChatCompletionMessage) cogito.Fragment {
	if f.ConversationMessages == nil {
		f.Messages = append(f.Messages, msg)
		return f
	}
	return f.AppendMessages(cogito.Message{Portable: msg})
}
