package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

// newModelSwitchTestModel builds a Model wired to a session whose endpoint
// advertises the given model IDs. The first ID is the session's current model.
// No turn is ever started, so the chat-completions side is never exercised and
// no TUI is launched.
func newModelSwitchTestModel(t *testing.T, ids ...string) Model {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := make([]map[string]string, 0, len(ids))
		for _, id := range ids {
			data = append(data, map[string]string{"id": id, "object": "model"})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	}))
	t.Cleanup(srv.Close)

	cfg := types.Config{Model: ids[0], BaseURL: srv.URL + "/v1"}
	s, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	m := newQueueTestModel()
	m.ctx = context.Background()
	m.cfg = cfg
	m.session = s
	return m
}

func lastMessage(t *testing.T, m Model) ChatMessage {
	t.Helper()
	if len(m.messages) == 0 {
		t.Fatal("no message was posted to the transcript")
	}
	return m.messages[len(m.messages)-1]
}

func TestDispatchModelListPostsTheListing(t *testing.T) {
	m := newModelSwitchTestModel(t, "model-a", "model-b")

	if cmd := m.dispatchResolved("/models"); cmd != nil {
		t.Fatal("/models must not start a turn")
	}
	msg := lastMessage(t, m)
	if msg.Role != "agent" {
		t.Fatalf("listing posted as %q, want agent", msg.Role)
	}
	if !strings.Contains(msg.Content, "* model-a") || !strings.Contains(msg.Content, "  model-b") {
		t.Fatalf("listing = %q, want the current model marked", msg.Content)
	}

	// The transcript renders an "agent" line as markdown, which is where a
	// plain listing loses its indent and its marker column. Assert on what the
	// user actually sees, not just on what was appended.
	rendered := renderMarkdownWith(m.markdownFor(70), msg.Content, 70)
	if !strings.Contains(rendered, "* model-a") {
		t.Fatalf("the current-model marker did not survive rendering: %q", rendered)
	}
	if !strings.Contains(rendered, "  model-b") {
		t.Fatalf("the listing lost its column alignment when rendered: %q", rendered)
	}
}

// Bare /model lists rather than erroring, so a user who forgets the name gets
// the menu.
func TestDispatchBareModelLists(t *testing.T) {
	m := newModelSwitchTestModel(t, "model-a", "model-b")

	if cmd := m.dispatchResolved("/model"); cmd != nil {
		t.Fatal("bare /model must not start a turn")
	}
	if msg := lastMessage(t, m); !strings.Contains(msg.Content, "model-b") {
		t.Fatalf("bare /model posted %q, want the listing", msg.Content)
	}
}

func TestDispatchModelSetSwitchesTheSession(t *testing.T) {
	m := newModelSwitchTestModel(t, "model-a", "model-b")

	if cmd := m.dispatchResolved("/model model-b"); cmd != nil {
		t.Fatal("/model <name> must not start a turn")
	}
	msg := lastMessage(t, m)
	if msg.Role != "agent" || !strings.Contains(msg.Content, "model-b") {
		t.Fatalf("confirmation = %+v, want an agent line naming model-b", msg)
	}
	if got := m.session.Model(); got != "model-b" {
		t.Fatalf("session model = %q, want model-b", got)
	}
}

// A typo must be refused in the transcript, as an error, with the session left
// where it was: SetModel takes no error, so this is the only place the mistake
// can still be caught before the next turn 404s.
//
// The refusal arrives as TWO lines. The listing rides an "agent" line inside a
// fence because the "error" role is word-wrapped, and a wrapped listing loses
// the indent column and clips long model IDs, which is the same failure the
// plain markdown path had. The names here are deliberately long and the render
// width deliberately narrow, so a regression shows up.
func TestDispatchModelSetRejectsAnUnknownName(t *testing.T) {
	const long = "hf.co/unsloth/gemma-4-27b-GGUF:Q4_K_M"
	m := newModelSwitchTestModel(t, "qwen3-coder-30b", long)

	if cmd := m.dispatchResolved("/model qwen3-codr-30b"); cmd != nil {
		t.Fatal("a refused /model must not start a turn")
	}
	if len(m.messages) < 2 {
		t.Fatalf("want a headline and a listing, got %d message(s): %+v", len(m.messages), m.messages)
	}
	headline, listing := m.messages[len(m.messages)-2], m.messages[len(m.messages)-1]

	if headline.Role != "error" {
		t.Fatalf("headline posted as %q, want error", headline.Role)
	}
	if !strings.Contains(headline.Content, "qwen3-codr-30b") {
		t.Fatalf("headline = %q, want it to name the typo", headline.Content)
	}
	if strings.Contains(headline.Content, long) {
		t.Fatalf("the listing must not ride on the wrapped error line: %q", headline.Content)
	}
	if listing.Role != "agent" {
		t.Fatalf("listing posted as %q, want agent", listing.Role)
	}

	// 30 columns: narrow enough that the long ID cannot fit, which is where a
	// re-wrapped listing loses its indent and its marker column.
	rendered := renderMarkdownWith(m.markdownFor(30), listing.Content, 30)
	if !strings.Contains(rendered, "* qwen3-coder-30b") {
		t.Fatalf("the current-model marker did not survive a narrow render: %q", rendered)
	}
	if !strings.Contains(rendered, "  hf.co/unsloth/gemma-4-27b-") {
		t.Fatalf("the alternative lost its indent at a narrow width: %q", rendered)
	}
	if got := m.session.Model(); got != "qwen3-coder-30b" {
		t.Fatalf("session model = %q, want the switch refused", got)
	}
}
