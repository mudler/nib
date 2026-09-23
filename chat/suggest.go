package chat

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/mudler/nib/classify"
	"github.com/mudler/nib/types"
)

// Suggestion is one reply the user might send next.
type Suggestion struct {
	Text       string
	Confidence float64
}

// SuggestInput is what a Suggester predicts from.
type SuggestInput struct {
	LastAssistant string   // the message the user is replying to
	RecentUser    []string // the user's recent messages, newest first
}

// Suggester predicts what the user would type next to keep the session
// going. It returns candidates best first; empty means no suggestion.
type Suggester interface {
	Suggest(ctx context.Context, in SuggestInput) ([]Suggestion, error)
}

const (
	// maxSuggestState caps the assistant text the suggester reads. The end
	// of a reply is where it hands over to the user.
	maxSuggestState = 4 << 10
	// maxRecentUser and maxRecentUserLen bound the user's own messages
	// offered back as candidates: short replies are the ones worth reusing.
	maxRecentUser    = 5
	maxRecentUserLen = 80
	// offerThreshold is the minimum confidence of an extracted answer option.
	offerThreshold          = 0.5
	defaultSuggestThreshold = 0.5
	defaultSuggestDelay     = 500 * time.Millisecond
)

var defaultReplies = []string{"continue", "yes, go ahead", "run the tests", "fix it", "commit it"}

type classifierSuggester struct {
	c         classify.Classifier
	replies   []string
	threshold float64
}

// NewClassifierSuggester returns a Suggester that ranks candidate replies
// with c: stock replies, the user's recent short messages, and the options
// the assistant asked the user to choose between.
func NewClassifierSuggester(c classify.Classifier, cfg types.SuggestionsConfig) Suggester {
	s := &classifierSuggester{c: c, replies: cfg.Replies, threshold: cfg.Threshold}
	if len(s.replies) == 0 {
		s.replies = defaultReplies
	}
	if s.threshold <= 0 {
		s.threshold = defaultSuggestThreshold
	}
	return s
}

func (s *classifierSuggester) Suggest(ctx context.Context, in SuggestInput) ([]Suggestion, error) {
	text := strings.TrimSpace(in.LastAssistant)
	if text == "" {
		return nil, nil
	}
	if len(text) > maxSuggestState {
		text = strings.ToValidUTF8(text[len(text)-maxSuggestState:], "")
	}

	ans, err := s.c.Classify(ctx, text, map[string]classify.Question{
		"offers": {Type: classify.TypeNoul, Instructions: "an option the user is asked to choose between"},
	})
	if err != nil {
		return nil, err
	}

	var cands []string
	seen := map[string]bool{}
	add := func(c string) bool {
		c = strings.TrimSpace(c)
		k := strings.ToLower(c)
		if c == "" || seen[k] {
			return false
		}
		seen[k] = true
		cands = append(cands, c)
		return true
	}
	for _, r := range s.replies {
		add(r)
	}
	recent := 0
	for _, u := range in.RecentUser {
		if recent == maxRecentUser {
			break
		}
		if len(u) <= maxRecentUserLen && !strings.Contains(u, "\n") && add(u) {
			recent++
		}
	}
	for _, e := range ans["offers"].Entities {
		if e.Confidence >= offerThreshold {
			add(e.Text)
		}
	}

	choices := make(map[string]string, len(cands))
	for _, c := range cands {
		choices[c] = ""
	}
	ans, err = s.c.Classify(ctx, text, map[string]classify.Question{
		"reply": {Type: classify.TypeChoice, Instructions: "the reply the user sends next to keep the work going", Choices: choices},
	})
	if err != nil {
		return nil, err
	}
	reply := ans["reply"]
	probs := reply.Probabilities
	if len(probs) == 0 && reply.Choice != "" {
		probs = map[string]float64{reply.Choice: reply.Confidence}
	}
	var out []Suggestion
	for _, c := range cands {
		if p := probs[c]; p >= s.threshold {
			out = append(out, Suggestion{Text: c, Confidence: p})
		}
	}
	slices.SortStableFunc(out, func(a, b Suggestion) int {
		switch {
		case a.Confidence > b.Confidence:
			return -1
		case a.Confidence < b.Confidence:
			return 1
		}
		return 0
	})
	return out, nil
}

// Suggest predicts the user's next reply from the conversation so far. It
// returns nil, nil when suggestions are off or there is nothing to reply to.
func (s *Session) Suggest(ctx context.Context) ([]Suggestion, error) {
	if s.suggester == nil {
		return nil, nil
	}
	s.historyMu.Lock()
	var in SuggestInput
	for i := len(s.messages) - 1; i >= 0; i-- {
		m := s.messages[i]
		switch {
		case m.Role == "assistant" && in.LastAssistant == "" && strings.TrimSpace(m.Content) != "":
			in.LastAssistant = m.Content
		case m.Role == "user" && m.Content != "":
			in.RecentUser = append(in.RecentUser, strings.TrimSpace(m.Content))
		}
	}
	s.historyMu.Unlock()
	if in.LastAssistant == "" {
		return nil, nil
	}
	return s.suggester.Suggest(ctx, in)
}

// SuggestionsEnabled reports whether Suggest can return anything.
func (s *Session) SuggestionsEnabled() bool { return s.suggester != nil }

// SuggestionDelay is how long the session must wait on the user, with no
// key press, before the UI asks for a suggestion.
func (s *Session) SuggestionDelay() time.Duration {
	if s.suggestDelay > 0 {
		return s.suggestDelay
	}
	return defaultSuggestDelay
}
