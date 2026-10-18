package chat

import (
	"reflect"

	"github.com/mudler/cogito"
	"github.com/sashabaranov/go-openai"
)

// replaceMessages returns f with its message list replaced by msgs.
//
// cogito keeps two views of a fragment: the authoritative conversation, which
// carries provider data, and the legacy Messages projection. Writing Messages
// alone leaves the two out of step, and cogito then refuses the fragment. This
// rebuilds the authoritative history instead. The common prefix and suffix of
// the old and new lists keep their provider envelopes, so an edit in the middle
// or a compaction does not drop provider data from the untouched messages.
//
// A legacy fragment, or one whose two views already disagree, takes msgs as
// its Messages and no authoritative history.
func replaceMessages(f cogito.Fragment, msgs []openai.ChatCompletionMessage) cogito.Fragment {
	history, err := f.Conversation()
	if f.ConversationMessages == nil || err != nil {
		f.ConversationMessages = nil
		f.Messages = msgs
		return f
	}

	prefix := 0
	for prefix < len(history) && prefix < len(msgs) &&
		reflect.DeepEqual(history[prefix].Portable, msgs[prefix]) {
		prefix++
	}
	suffix := 0
	for suffix < len(history)-prefix && suffix < len(msgs)-prefix &&
		reflect.DeepEqual(history[len(history)-1-suffix].Portable, msgs[len(msgs)-1-suffix]) {
		suffix++
	}

	out := make([]cogito.Message, 0, len(msgs))
	out = append(out, history[:prefix]...)
	out = append(out, cogito.WrapMessages(msgs[prefix:len(msgs)-suffix])...)
	out = append(out, history[len(history)-suffix:]...)
	return f.WithConversation(out)
}
