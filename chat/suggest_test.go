package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mudler/nib/classify"
	"github.com/mudler/nib/types"
	openai "github.com/sashabaranov/go-openai"
)

// suggestClassifier answers the offers question with offers and ranks the
// reply options with probs (missing options get 0).
type suggestClassifier struct {
	offers []classify.Entity
	probs  map[string]float64
	err    error
	last   map[string]classify.Question
	state  string
}

func (f *suggestClassifier) Classify(_ context.Context, state string, qs map[string]classify.Question) (map[string]classify.Answer, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.last, f.state = qs, state
	out := map[string]classify.Answer{}
	if _, ok := qs["offers"]; ok {
		out["offers"] = classify.Answer{Noul: 1, Entities: f.offers}
	}
	if q, ok := qs["reply"]; ok {
		p := map[string]float64{}
		best, bestP := "", -1.0
		for name := range q.Choices {
			p[name] = f.probs[name]
			if p[name] > bestP {
				best, bestP = name, p[name]
			}
		}
		out["reply"] = classify.Answer{Choice: best, Confidence: bestP, Probabilities: p}
	}
	return out, nil
}

func texts(ss []Suggestion) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Text)
	}
	return out
}

func TestSuggestRanksCandidates(t *testing.T) {
	f := &suggestClassifier{
		offers: []classify.Entity{{Text: "Postgres", Confidence: 0.9}, {Text: "SQLite", Confidence: 0.8}, {Text: "noise", Confidence: 0.2}},
		probs:  map[string]float64{"Postgres": 0.6, "continue": 0.2, "ship it": 0.7, "SQLite": 0.55},
	}
	sg := NewClassifierSuggester(f, types.SuggestionsConfig{})
	got, err := sg.Suggest(context.Background(), SuggestInput{
		LastAssistant: "Use Postgres or SQLite?",
		RecentUser:    []string{"ship it"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(texts(got), "|") != "ship it|Postgres|SQLite" {
		t.Fatalf("suggestions = %v", texts(got))
	}
	if _, ok := f.last["reply"].Choices["noise"]; ok {
		t.Fatal("a low-confidence span became a candidate")
	}
	for _, stock := range []string{"continue", "yes, go ahead", "run the tests", "fix it", "commit it"} {
		if _, ok := f.last["reply"].Choices[stock]; !ok {
			t.Errorf("stock reply %q missing from the candidates", stock)
		}
	}
}

func TestSuggestCandidatesDedupAndCaps(t *testing.T) {
	f := &suggestClassifier{offers: []classify.Entity{{Text: "Continue", Confidence: 0.9}}}
	long := strings.Repeat("x", 81)
	sg := NewClassifierSuggester(f, types.SuggestionsConfig{Replies: []string{"continue"}})
	_, err := sg.Suggest(context.Background(), SuggestInput{
		LastAssistant: "Continue?",
		RecentUser:    []string{"a", "b", "c", "d", "e", "f", long, "CONTINUE"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ch := f.last["reply"].Choices
	if _, ok := ch[long]; ok {
		t.Fatal("a message over 80 characters became a candidate")
	}
	n := 0
	for c := range ch {
		if strings.EqualFold(c, "continue") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("continue appears %d times in %v", n, ch)
	}
	// At most 5 recent user messages: of a..f only 5 survive.
	recent := 0
	for _, c := range []string{"a", "b", "c", "d", "e", "f"} {
		if _, ok := ch[c]; ok {
			recent++
		}
	}
	if recent != 5 {
		t.Fatalf("%d recent messages are candidates, want 5: %v", recent, ch)
	}
}

func TestSuggestThreshold(t *testing.T) {
	f := &suggestClassifier{probs: map[string]float64{"continue": 0.4}}
	got, err := NewClassifierSuggester(f, types.SuggestionsConfig{}).Suggest(context.Background(), SuggestInput{LastAssistant: "Done."})
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v; want nothing below 0.5", got, err)
	}
	got, _ = NewClassifierSuggester(f, types.SuggestionsConfig{Threshold: 0.3}).Suggest(context.Background(), SuggestInput{LastAssistant: "Done."})
	if len(got) != 1 || got[0].Text != "continue" {
		t.Fatalf("configured threshold ignored: %v", got)
	}
}

func TestSuggestEmptyMessage(t *testing.T) {
	f := &suggestClassifier{err: errors.New("must not be called")}
	got, err := NewClassifierSuggester(f, types.SuggestionsConfig{}).Suggest(context.Background(), SuggestInput{LastAssistant: "  "})
	if err != nil || got != nil {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestSuggestError(t *testing.T) {
	f := &suggestClassifier{err: errors.New("down")}
	if _, err := NewClassifierSuggester(f, types.SuggestionsConfig{}).Suggest(context.Background(), SuggestInput{LastAssistant: "hi"}); err == nil {
		t.Fatal("want the classifier error")
	}
}

func TestSuggestStateIsTail(t *testing.T) {
	f := &suggestClassifier{}
	msg := strings.Repeat("a", 10_000) + "THE END"
	_, _ = NewClassifierSuggester(f, types.SuggestionsConfig{}).Suggest(context.Background(), SuggestInput{LastAssistant: msg})
	if len(f.state) > maxSuggestState || !strings.HasSuffix(f.state, "THE END") {
		t.Fatalf("state len %d, tail kept = %v", len(f.state), strings.HasSuffix(f.state, "THE END"))
	}
}

func TestSessionSuggestUsesHistory(t *testing.T) {
	f := &suggestClassifier{probs: map[string]float64{"go on": 0.9}}
	s := &Session{}
	s.setClassifierState(&classifierState{suggester: NewClassifierSuggester(f, types.SuggestionsConfig{})})
	s.messages = []openai.ChatCompletionMessage{
		{Role: "user", Content: "go on"},
		{Role: "assistant", Content: "Step one done. Next?"},
		{Role: "tool", Content: "ignored"},
	}
	got, err := s.Suggest(context.Background())
	if err != nil || len(got) == 0 || got[0].Text != "go on" {
		t.Fatalf("got %v, %v", got, err)
	}
	if !strings.Contains(f.state, "Step one done") {
		t.Fatalf("state = %q", f.state)
	}
}

func TestSessionSuggestWithoutSuggester(t *testing.T) {
	s := &Session{}
	if got, err := s.Suggest(context.Background()); got != nil || err != nil {
		t.Fatalf("got %v, %v", got, err)
	}
	if s.SuggestionsEnabled() {
		t.Fatal("no suggester, yet enabled")
	}
}
