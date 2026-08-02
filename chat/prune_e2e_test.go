package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
	openai "github.com/sashabaranov/go-openai"
)

// pruneRecorder is a fake OpenAI endpoint that decodes and keeps every request
// it is sent, and always answers with a plain stop message.
//
// It records the DECODED request rather than only the raw bytes so assertions
// can be made per message — "every tool result in the final request is a stub"
// is a different and stronger claim than "the word stub appears somewhere in the
// bytes of some request". The raw body is kept too, so a decode failure is
// visible as a decode failure instead of as a mysteriously empty request.
type pruneRecorder struct {
	mu         sync.Mutex
	reqs       []openai.ChatCompletionRequest
	bodies     []string
	decodeErrs []error
}

func (p *pruneRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req openai.ChatCompletionRequest
	err := json.Unmarshal(body, &req)

	p.mu.Lock()
	p.bodies = append(p.bodies, string(body))
	p.reqs = append(p.reqs, req)
	if err != nil {
		p.decodeErrs = append(p.decodeErrs, err)
	}
	p.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "fake", "object": "chat.completion", "model": "fake",
		"choices": []any{map[string]any{
			"index": 0, "message": map[string]any{"role": "assistant", "content": "ok"},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	})
}

// all returns every request body as one string, for claims about what did or did
// not go out over the whole turn.
func (p *pruneRecorder) all() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.bodies, "\n")
}

// last returns the final decoded request, which is the one carrying the fullest
// conversation and therefore the one worth asserting on.
func (p *pruneRecorder) last(t *testing.T) openai.ChatCompletionRequest {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.decodeErrs) > 0 {
		t.Fatalf("a request body did not decode as a ChatCompletionRequest: %v", p.decodeErrs[0])
	}
	if len(p.reqs) == 0 {
		t.Fatal("the fake LLM received no request")
	}
	return p.reqs[len(p.reqs)-1]
}

// prunedE2ESession builds a session whose seeded history already holds oversized
// read results, pointed at a recording fake LLM. marker appears in each seeded
// result and nowhere else, so finding it in a request means the original content
// went out verbatim.
func prunedE2ESession(t *testing.T, rec *pruneRecorder, marker string, results int) *Session {
	t.Helper()
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)

	body := marker + strings.Repeat("x", 9000)
	var hist []openai.ChatCompletionMessage
	hist = append(hist, openai.ChatCompletionMessage{Role: "user", Content: "read the package"})
	for i := range results {
		id := string(rune('a' + i))
		hist = append(hist,
			callMsg(id, "read", `{"path":"`+id+`.go"}`),
			resultMsg(id, body),
		)
	}
	// A trailing assistant turn keeps the results out of the protected trailing
	// tool run, where the policy would refuse to touch them and the test would
	// prove nothing.
	hist = append(hist, openai.ChatCompletionMessage{Role: "assistant", Content: "read them"})

	cfg := types.Config{
		Model:        "fake-model",
		APIKey:       "fake-key",
		BaseURL:      srv.URL + "/v1",
		LogLevel:     "error",
		ApprovalMode: "auto",
		AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 3},
		ToolOutputPruning: types.ToolOutputPruningConfig{
			HighWaterTokens: 1000, LowWaterTokens: 1, MinResultTokens: 1,
		},
		InitialHistory: hist,
	}

	session, err := NewSession(context.Background(), cfg, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

// The end-to-end proof that the manipulator is installed on the RUN options: a
// real NewSession and a real SendMessage, asserted on the request the LLM
// actually received rather than on what nib meant to send. Deleting the
// cogito.WithMessagesManipulator line in session.go fails here.
func TestSessionSendMessagePrunesOnTheWire(t *testing.T) {
	rec := &pruneRecorder{}
	const marker = "SENTINEL_FILE_CONTENTS"
	session := prunedE2ESession(t, rec, marker, 2)

	if _, err := session.SendMessage("now what?"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	last := rec.last(t)
	var toolBodies []string
	for _, m := range last.Messages {
		if m.Role == "tool" {
			toolBodies = append(toolBodies, m.Content)
		}
	}
	// Without this the rest of the test passes vacuously the day a change stops
	// sending tool results at all.
	if len(toolBodies) != 2 {
		t.Fatalf("the request carried %d tool results, want 2: the test would prove nothing", len(toolBodies))
	}
	for i, b := range toolBodies {
		if strings.Contains(b, marker) {
			t.Fatalf("tool result %d went out verbatim: %.80q", i, b)
		}
		if !strings.Contains(b, "dropped to save context") {
			t.Fatalf("tool result %d is not a stub: %.80q", i, b)
		}
	}
	// Not merely absent from the final request: the oversized content never left
	// the process on any request of the turn.
	if strings.Contains(rec.all(), marker) {
		t.Fatal("an oversized tool result went out on the wire unpruned")
	}
}

// The other half of the contract. Pruning rewrites the request, never the
// session's own state — a copy that turned out to alias s.fragment, or a
// manipulated fragment handed back by cogito and stored, would silently destroy
// the content the transcript, ExportHistory and compaction still need.
//
// This one passes whether or not the manipulator is installed, by design: it
// pins that pruning is harmless, not that it happened.
func TestSessionPruningDoesNotAlterStoredHistory(t *testing.T) {
	rec := &pruneRecorder{}
	const marker = "SENTINEL_FILE_CONTENTS"
	session := prunedE2ESession(t, rec, marker, 2)

	if _, err := session.SendMessage("now what?"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	// Guard the guard: if nothing was pruned this turn, "history intact" is not
	// evidence of anything.
	if !strings.Contains(rec.all(), "dropped to save context") {
		t.Fatal("nothing was pruned, so an intact history proves nothing")
	}

	// s.fragment is the real model context carried into the next turn.
	intact := 0
	for _, m := range session.fragment.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, marker) {
			intact++
		}
	}
	if intact != 2 {
		t.Fatalf("s.fragment holds %d intact tool results, want 2: pruning mutated the model context", intact)
	}

	// And s.messages, the parallel log ExportHistory and the transcript read.
	exported := 0
	for _, m := range session.ExportHistory() {
		if m.Role == "tool" && strings.Contains(m.Content, marker) {
			exported++
		}
	}
	if exported != 2 {
		t.Fatalf("ExportHistory returned %d intact tool results, want 2: a resumed session would lose the real content", exported)
	}
}
