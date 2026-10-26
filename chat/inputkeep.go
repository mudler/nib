package chat

import (
	"strings"

	"github.com/mudler/cogito"
	"github.com/sashabaranov/go-openai"
)

// keepInput remembers the texts of a batch the model has seen, until the run
// that saw it is committed to the history (see addKeptInput).
func (s *Session) keepInput(texts []string) {
	s.keptMu.Lock()
	defer s.keptMu.Unlock()
	s.kept = append(s.kept, strings.Join(texts, "\n\n"))
}

// addKeptInput adds the batches the model saw during the run as user messages
// of f. They went to the model on one request each, after the messages the run
// had then, so they go before the run's last answer when it ends with one, and
// at the end otherwise.
func (s *Session) addKeptInput(f cogito.Fragment) cogito.Fragment {
	s.keptMu.Lock()
	batches := s.kept
	s.kept = nil
	s.keptMu.Unlock()
	if len(batches) == 0 {
		return f
	}
	msgs := append([]openai.ChatCompletionMessage(nil), f.Messages...)
	var added []openai.ChatCompletionMessage
	for _, b := range batches {
		added = append(added, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: b})
	}
	at := len(msgs)
	if n := len(msgs); n > 0 && msgs[n-1].Role == openai.ChatMessageRoleAssistant && len(msgs[n-1].ToolCalls) == 0 {
		at = n - 1
	}
	out := append(append(append([]openai.ChatCompletionMessage(nil), msgs[:at]...), added...), msgs[at:]...)
	return replaceMessages(f, out)
}
