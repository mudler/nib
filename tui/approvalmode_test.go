package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
	"github.com/mudler/nib/types"
)

func TestNextApprovalMode(t *testing.T) {
	for _, tc := range []struct {
		current, base string
		classifier    bool
		want          string
	}{
		{"prompt", "prompt", true, "classify"},
		{"classify", "prompt", true, "auto"},
		{"auto", "prompt", true, "prompt"},
		{"strict", "strict", true, "classify"},
		{"auto", "strict", true, "strict"},
		{"allowlist", "allowlist", true, "classify"},
		{"prompt", "prompt", false, "auto"}, // no classifier: classify is skipped
		{"auto", "prompt", false, "prompt"},
	} {
		if got := nextApprovalMode(tc.current, tc.base, tc.classifier); got != tc.want {
			t.Errorf("next(%q, base %q, classifier %v) = %q, want %q", tc.current, tc.base, tc.classifier, got, tc.want)
		}
	}
}

func TestBaseApprovalMode(t *testing.T) {
	for in, want := range map[string]string{"": "prompt", "prompt": "prompt", "strict": "strict", "allowlist": "allowlist", "classify": "prompt", "auto": "prompt"} {
		if got := baseApprovalMode(in); got != want {
			t.Errorf("base(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHeaderShowsClassifyBadge(t *testing.T) {
	h := render.Base{}.Header(render.ViewState{Brand: "nib", ApprovalMode: "classify", Width: 80})
	if !strings.Contains(h, "classify") {
		t.Fatalf("header lacks the classify badge: %q", h)
	}
	h = render.Base{}.Header(render.ViewState{Brand: "nib", ApprovalMode: "prompt", Width: 80})
	if strings.Contains(h, "classify") || strings.Contains(h, "prompt") {
		t.Fatalf("prompt mode must not show a badge: %q", h)
	}
}

func TestApprovalContentShowsVerdict(t *testing.T) {
	c := buildApprovalContent(chat.ToolCallRequest{Name: "bash", Arguments: `{"script":"rm -rf build"}`, Verdict: "destructive (0.82)"})
	if !strings.Contains(c.meta, "classifier: destructive (0.82)") {
		t.Fatalf("meta = %q", c.meta)
	}
}

func classifierSession(t *testing.T) *chat.Session {
	t.Helper()
	s, err := chat.NewSession(context.Background(), types.Config{
		BaseURL:    "http://127.0.0.1:1/v1",
		Classifier: types.ClassifierConfig{Model: "gliner"},
	}, chat.Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestDispatchApproveSetsMode(t *testing.T) {
	m := newQueueTestModel()
	m.session = classifierSession(t)
	if cmd := m.dispatchResolved("/approve classify"); cmd != nil {
		t.Fatal("/approve must not start a turn")
	}
	if m.session.ApprovalMode() != "classify" {
		t.Fatalf("mode = %q", m.session.ApprovalMode())
	}
	if msg := lastMessage(t, m); msg.Content != fmt.Sprintf(theme.ApproveModeNotice, "classify") {
		t.Fatalf("notice = %q", msg.Content)
	}
	m.dispatchResolved("/approve auto")
	if !m.session.AutoApprove() {
		t.Fatal("/approve auto must turn auto-approve on")
	}
	m.dispatchResolved("/approve prompt")
	if m.session.AutoApprove() || m.session.ApprovalMode() != "prompt" {
		t.Fatalf("auto = %v, mode = %q", m.session.AutoApprove(), m.session.ApprovalMode())
	}
}

func TestDispatchApproveBareShowsMode(t *testing.T) {
	m := newQueueTestModel()
	m.session = classifierSession(t)
	m.dispatchResolved("/approve")
	if msg := lastMessage(t, m); msg.Content != fmt.Sprintf(theme.ApproveModeNotice, "prompt") {
		t.Fatalf("notice = %q", msg.Content)
	}
}

func TestDispatchApproveClassifyWithoutClassifier(t *testing.T) {
	m := newQueueTestModel()
	m.session = &chat.Session{}
	m.dispatchResolved("/approve classify")
	if m.session.ApprovalMode() != "prompt" {
		t.Fatalf("mode = %q, want prompt kept", m.session.ApprovalMode())
	}
	if msg := lastMessage(t, m); msg.Role != "error" {
		t.Fatalf("want an error line, got %+v", msg)
	}
}

func TestShiftTabCyclesApprovalMode(t *testing.T) {
	m := newQueueTestModel()
	m.session = classifierSession(t)
	var want = []string{"classify", "auto", "prompt"}
	for _, w := range want {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
		m = next.(Model)
		if got := effectiveApprovalMode(m.session); got != w {
			t.Fatalf("after Shift+Tab mode = %q, want %q", got, w)
		}
	}
}

func TestYoloOffReturnsToClassify(t *testing.T) {
	m := newQueueTestModel()
	m.session = classifierSession(t)
	m.dispatchResolved("/approve classify")
	m.dispatchResolved("/yolo on")
	if vs := m.viewState(); !vs.AutoApprove {
		t.Fatal("yolo badge missing")
	}
	m.dispatchResolved("/yolo off")
	if vs := m.viewState(); vs.AutoApprove || vs.ApprovalMode != "classify" {
		t.Fatalf("after /yolo off: auto = %v, mode = %q", vs.AutoApprove, vs.ApprovalMode)
	}
}

func TestAutoApprovedLineInTranscript(t *testing.T) {
	m := newQueueTestModel()
	m.ctx = context.Background()
	next, _ := m.Update(autoApprovedMsg{
		req:     chat.ToolCallRequest{Name: "bash", Arguments: `{"script":"go test ./..."}`},
		verdict: chat.Verdict{Category: "build_test", Confidence: 0.93, Approved: true},
	})
	m = next.(Model)
	msg := lastMessage(t, m)
	if !strings.Contains(msg.Content, "build_test 0.93") || !strings.Contains(msg.Content, "go test ./...") {
		t.Fatalf("line = %q", msg.Content)
	}
}
