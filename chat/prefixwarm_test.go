package chat

import (
	"context"
	"testing"

	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"
)

// TestPrefixWarmStartsFalse: a freshly built session has never issued a
// request, so nothing of its prefix is in the server's cache. A rebuilt
// session is a new *Session, which is why no reset API is needed.
func TestPrefixWarmStartsFalse(t *testing.T) {
	s := newWarmTestSession(t)
	if s.PrefixWarm() {
		t.Fatal("a fresh session must not report its prefix as warm")
	}
}

// TestPrefixWarmAfterSendMessage: a completed turn went through the model, so
// the prefix has been prefilled.
func TestPrefixWarmAfterSendMessage(t *testing.T) {
	s := newWarmTestSession(t)
	if _, err := s.SendMessage("hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if !s.PrefixWarm() {
		t.Fatal("a completed SendMessage must leave the prefix warm")
	}
}

// TestPrefixWarmAfterWarm: priming is the whole point of Warm, so a successful
// prime must report warm.
func TestPrefixWarmAfterWarm(t *testing.T) {
	s := newWarmTestSession(t)
	if err := s.Warm(context.Background()); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	if !s.PrefixWarm() {
		t.Fatal("a successful Warm must leave the prefix warm")
	}
}

// TestPrefixWarmNotSetByFailedWarm: a prime that never reached the model (here,
// a cancelled context) prefilled nothing.
func TestPrefixWarmNotSetByFailedWarm(t *testing.T) {
	s := newWarmTestSession(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Warm(ctx); err == nil {
		t.Fatal("Warm on a cancelled context must return an error")
	}
	if s.PrefixWarm() {
		t.Fatal("a Warm that never reached the model must not report warm")
	}
}

// TestPrefixWarmNotSetWhenNothingIsSendable is the case that motivated this
// API. SendWithAttachments returns early — without reaching SendMessage — when
// every attached file is blocked and there is no user text. Its return shape
// ("", blocked, nil) is indistinguishable at the call site from a real send
// that answered empty while blocking files, so a caller that marks the session
// warm after every SendWithAttachments loses the label on the NEXT turn, which
// is the genuinely cold one. The session must own this truth.
func TestPrefixWarmNotSetWhenNothingIsSendable(t *testing.T) {
	s := newWarmTestSession(t)
	llm := s.llm.(*captureLLM)

	// Empty baseURL ⇒ capability lookup fails ⇒ text-only model ⇒ an image is
	// blocked. With no user text, nothing is sendable.
	reply, blocked, err := s.SendWithAttachments(context.Background(), "", []string{"/nonexistent/shot.png"}, nil)
	if err != nil {
		t.Fatalf("SendWithAttachments: %v", err)
	}
	if reply != "" || len(blocked) != 1 {
		t.Fatalf("expected the nothing-sendable return, got reply=%q blocked=%+v", reply, blocked)
	}
	if llm.calls() != 0 {
		t.Fatalf("guard: nothing should have reached the LLM, got %d call(s)", llm.calls())
	}
	if s.PrefixWarm() {
		t.Fatal("an all-blocked, no-text SendWithAttachments prefilled nothing and must not report warm")
	}
}

// TestPrefixWarmNotSetWhenAttachmentsFail covers the other early return: an
// attachments.Apply error (here, a text file that cannot be read) bails before
// SendMessage, and its error is shape-identical to a SendMessage error.
func TestPrefixWarmNotSetWhenAttachmentsFail(t *testing.T) {
	s := newWarmTestSession(t)
	llm := s.llm.(*captureLLM)

	if _, _, err := s.SendWithAttachments(context.Background(), "summarize", []string{"/nonexistent/notes.txt"}, nil); err == nil {
		t.Fatal("guard: reading a nonexistent text attachment must fail")
	}
	if llm.calls() != 0 {
		t.Fatalf("guard: nothing should have reached the LLM, got %d call(s)", llm.calls())
	}
	if s.PrefixWarm() {
		t.Fatal("a failed attachment pass prefilled nothing and must not report warm")
	}
}

// blockingLLM parks in the request until the turn context is cancelled, so a
// test can interrupt a turn that is provably mid-flight.
type blockingLLM struct{ started chan struct{} }

func (b *blockingLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return cogito.LLMReply{}, cogito.LLMUsage{}, ctx.Err()
}

func (b *blockingLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	<-ctx.Done()
	return f, ctx.Err()
}

// TestPrefixWarmNotSetByInterruptedTurn pins the conservative choice: a turn
// cancelled before it produced an answer does not count as warm. Cancellation
// after the request reached the server may or may not have populated the
// cache, and the two mistakes are not symmetric — staying cold costs one extra
// "preparing the model" label, while a wrong "warm" costs the user a silent
// minute with no explanation.
func TestPrefixWarmNotSetByInterruptedTurn(t *testing.T) {
	s := newWarmTestSession(t)
	s.llm = &blockingLLM{started: make(chan struct{}, 1)}

	done := make(chan struct{})
	go func() {
		defer close(done)
		<-s.llm.(*blockingLLM).started
		s.Interrupt()
	}()

	if _, err := s.SendMessage("hello"); err == nil {
		t.Fatal("an interrupted turn must return an error")
	}
	<-done
	if s.PrefixWarm() {
		t.Fatal("a turn interrupted before it answered must not report warm")
	}
}

// TestPrefixWarmIsSafeUnderConcurrentReads: the UI polls PrefixWarm from
// another goroutine while a turn runs; that must not race.
func TestPrefixWarmIsSafeUnderConcurrentReads(t *testing.T) {
	s := newWarmTestSession(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			_ = s.PrefixWarm()
		}
	}()
	if _, err := s.SendMessage("hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	<-done
	if !s.PrefixWarm() {
		t.Fatal("a completed SendMessage must leave the prefix warm")
	}
}
