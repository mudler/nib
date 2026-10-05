package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mudler/nib/tui/render"
	"github.com/mudler/nib/tui/render/full"
	"github.com/mudler/nib/tui/render/inline"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

// Leave one counted reminder behind a provider failure, without pausing.
func repromptModel(t *testing.T) (Model, *atomic.Int32, func()) {
	t.Helper()
	var calls, failAt atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			fmt.Fprint(w, `{"data":[]}`)
			return
		}
		n := calls.Add(1)
		if n == failAt.Load() {
			http.Error(w, "test failure", 401)
			return
		}
		var request struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"still working\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"still working"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)
	cfg := types.Config{Model: "fake", APIKey: "fake", BaseURL: srv.URL, BaseDir: t.TempDir(), LogLevel: "error", ApprovalMode: "auto", Goal: types.GoalConfig{MaxReprompts: 1, RepromptWindow: "1h"}, AgentOptions: types.AgentOptions{Iterations: 2, MaxAttempts: 1, MaxRetries: 1}}
	s, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	m := newQueueTestModel()
	m.session = s
	m.ctx = context.Background()
	m.cfg = cfg
	s.SetGoal("keep working")
	prime := func() {
		failAt.Store(calls.Load() + 2)
		if _, err := s.SendMessageWithDelivery("automatic", chat.InputAutomatic); err == nil {
			t.Fatal("expected provider failure")
		}
		if s.GoalPaused() {
			t.Fatal("prime paused goal")
		}
		failAt.Store(0)
	}
	return m, &calls, prime
}

func TestRepromptQueueAcceptsBeforeDelivery(t *testing.T) {
	m, calls, prime := repromptModel(t)
	prime()
	m.loading = true
	m.textarea.SetValue("human follow up")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	before := calls.Load()
	_, err := m.session.SendMessageWithDelivery("automatic probe", chat.InputAutomatic)
	if err != nil {
		t.Fatal(err)
	}
	if got := calls.Load() - before; got != 2 {
		t.Fatalf("accepted queued human did not reset budget: calls=%d want 2", got)
	}
}

func TestRepromptQueueDrainDoesNotAcceptAgain(t *testing.T) {
	m, calls, prime := repromptModel(t)
	m.loading = true
	m.textarea.SetValue("human follow up")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	prime()
	before := calls.Load()
	cmd := m.flushQueueAsTurn()
	if cmd == nil {
		t.Fatal("no queued turn")
	}
	result := cmd().(responseMsg)
	if result.err != nil {
		t.Fatal(result.err)
	}
	if got := calls.Load() - before; got != 1 {
		t.Fatalf("queue drain reset budget: calls=%d want 1", got)
	}
}

func TestRepromptAutomaticQueueAndWakeup(t *testing.T) {
	for _, mode := range []string{"queue", "wakeup", "kickoff"} {
		t.Run(mode, func(t *testing.T) {
			m, calls, prime := repromptModel(t)
			prime()
			before := calls.Load()
			var cmd tea.Cmd
			switch mode {
			case "queue":
				m.loading = true
				m.dispatchLoop("automatic")
				cmd = m.flushQueueAsTurn()
			case "wakeup":
				next, c := m.Update(wakeupFireMsg{prompt: "automatic", gen: m.wakeupGen})
				m = next.(Model)
				cmd = c
			case "kickoff":
				cmd = m.startGoalTurn("automatic")
			}
			// Update returns a batch for wakeups.
			var run func(tea.Cmd)
			run = func(c tea.Cmd) {
				if c == nil {
					return
				}
				switch msg := c().(type) {
				case tea.BatchMsg:
					for _, child := range msg {
						run(child)
					}
				case responseMsg:
					if msg.err != nil {
						t.Fatal(msg.err)
					}
				}
			}
			run(cmd)
			if got := calls.Load() - before; got != 1 {
				t.Fatalf("automatic %s reset budget: calls=%d", mode, got)
			}
		})
	}
}

func TestRepromptParkedLoopFailedInjectionRetainsOrigin(t *testing.T) {
	m := newGoalModel(t)
	m.loading = true
	m.parked = true // RunLive is false: injection cannot accept.
	m.dispatchLoop("automatic retry")
	if len(m.queue) != 1 || m.queue[0].delivery != chat.InputAutomatic || m.queue[0].text != "automatic retry" {
		t.Fatalf("failed automatic delivery lost payload/origin: %+v", m.queue)
	}
}

func TestGoalRepromptNoticeSurvivesResponse(t *testing.T) {
	for _, p := range []render.Presenter{inline.New(), full.New()} {
		t.Run(fmt.Sprintf("%T", p), func(t *testing.T) {
			m := newGoalModel(t)
			m.presenter = p
			m.goalPausedChan = make(chan chat.GoalPausedNotice, 1)
			m.session.SetGoal("keep working")
			m.session.PauseGoal()
			m.goalPausedChan <- chat.GoalPausedNotice{MaxReprompts: 3, Window: 30 * time.Second, Paused: true}
			m.appendStreamedContent("partial")
			next, _ := m.Update(responseMsg{content: "final answer"})
			m = next.(Model)
			if len(m.messages) != 2 || m.messages[0].Content != "final answer" || !strings.Contains(m.messages[1].Content, "3 automatic reminders within 30s") {
				t.Fatalf("reply/notice ordering: %+v", m.messages)
			}
			if !strings.Contains(m.messages[1].Content, "/goal resume") || !strings.Contains(m.messages[1].Content, "/goal clear") {
				t.Fatal(m.messages[1].Content)
			}
			next, _ = m.Update(responseMsg{content: "next answer"})
			m = next.(Model)
			count := 0
			for _, msg := range m.messages {
				if strings.Contains(msg.Content, "automatic reminders") {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("notice repeated/lost: %+v", m.messages)
			}
			m.dispatchInput("ordinary human text")
			if !m.session.GoalPaused() {
				t.Fatal("human input resumed goal")
			}
		})
	}
}

func TestGoalRepromptCallbackAutosaveRestoreAndResume(t *testing.T) {
	fixture, _, _ := repromptModel(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := NewModel(ctx, fixture.cfg, 24, nil, inline.New())
	ready := m.initSession()().(sessionReadyMsg)
	if ready.err != nil {
		t.Fatal(ready.err)
	}
	m.session = ready.session
	m.sessionReady = true
	defer m.session.Close()
	m.session.SetGoal("keep working")
	result := m.startGoalTurn("kickoff")().(responseMsg)
	if result.err != nil {
		t.Fatal(result.err)
	}
	if len(m.goalPausedChan) != 1 {
		t.Fatal("callback did not retain pause event before response")
	}
	m.appendStreamedContent("partial")
	next, _ := m.Update(result)
	m = next.(Model)
	if !m.session.GoalPaused() {
		t.Fatal("guard did not pause")
	}
	if len(m.messages) < 2 || m.messages[len(m.messages)-2].Content != result.content || !strings.Contains(m.messages[len(m.messages)-1].Content, "1 automatic reminders within 1h0m0s") {
		t.Fatalf("final response and notice: %+v", m.messages)
	}
	rec, err := m.store.Load(m.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.GoalPaused || rec.Goal != "keep working" {
		t.Fatalf("autosave lost pause: %+v", rec)
	}
	found := false
	for _, msg := range rec.Messages {
		if msg.Role == "assistant" && msg.Content == result.content {
			found = true
		}
	}
	if !found {
		t.Fatal("autosave omitted committed final response")
	}
	// An old record and unlimited startup policy cannot implicitly resume it.
	rec.Updated = time.Unix(1, 0)
	m.cfg.Goal.MaxReprompts = -1
	cmd := m.applyResume(rec)
	restored := cmd().(sessionReadyMsg)
	if restored.err != nil {
		t.Fatal(restored.err)
	}
	m.session = restored.session
	m.sessionReady = true
	defer m.session.Close()
	if !m.session.GoalPaused() {
		t.Fatal("restore resumed old paused goal")
	}
	result = m.sendMessageDelivery("automatic wakeup", chat.InputAutomatic)().(responseMsg)
	if result.err != nil || !m.session.GoalPaused() {
		t.Fatalf("automatic turn resumed: %v", result.err)
	}
	// Ordinary conversation resets counting, never paused state.
	m.dispatchInput("human conversation")
	if !m.session.GoalPaused() {
		t.Fatal("human input resumed restored goal")
	}
	if cmd := m.dispatchInput("/goal resume"); cmd == nil || m.session.GoalPaused() {
		t.Fatal("explicit resume did not start pursuit")
	}
}

func TestRepromptMixedQueueRedispatchAndEdit(t *testing.T) {
	t.Run("mixed-combination", func(t *testing.T) {
		m, calls, prime := repromptModel(t)
		m.loading = true
		m.dispatchLoop("automatic first")
		m.textarea.SetValue("human second")
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(Model)
		m.dispatchLoop("automatic third")
		m.redispatch = []string{"already accepted"}
		prime()
		before := calls.Load()
		result := m.flushQueueAsTurn()().(responseMsg)
		if result.err != nil {
			t.Fatal(result.err)
		}
		if calls.Load()-before != 1 {
			t.Fatal("combining/redispatch reset budget")
		}
		want := "already accepted\n\nautomatic first\n\nhuman second\n\nautomatic third"
		found := false
		for _, msg := range m.session.ExportHistory() {
			if msg.Role == "user" && msg.Content == want {
				found = true
			}
		}
		if !found {
			t.Fatal("mixed payload lost ordering")
		}
		if len(m.messages) != 3 {
			t.Fatalf("redispatch echoed twice: %+v", m.messages)
		}
	})
	t.Run("edit-is-new-submission", func(t *testing.T) {
		m, calls, prime := repromptModel(t)
		prime()
		m.loading = true
		m.dispatchLoop("automatic draft")
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
		m = next.(Model)
		if len(m.queue) != 0 || m.textarea.Value() != "automatic draft" {
			t.Fatal("edit lost source")
		}
		m.textarea.SetValue("edited human draft")
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(Model)
		if len(m.queue) != 1 || m.queue[0].delivery != chat.InputAccepted {
			t.Fatal("edited submission retained automatic origin")
		}
		before := calls.Load()
		_, err := m.session.SendMessageWithDelivery("probe", chat.InputAutomatic)
		if err != nil || calls.Load()-before != 2 {
			t.Fatalf("edited human acceptance did not reset: %v", err)
		}
	})
}

func TestRepromptSettingsPreserveLiveBudget(t *testing.T) {
	m, calls, prime := repromptModel(t)
	prime()
	settings, _ := newSettingsTestModel(t)
	m.cfg.BaseDir = settings.cfg.BaseDir
	for _, input := range []string{"/settings goal.max_reprompts -1", "/settings goal.reprompt_window 1ns", "/goal", "/help"} {
		m.dispatchInput(input)
	}
	before := calls.Load()
	result := m.sendMessageDelivery("automatic probe", chat.InputAutomatic)().(responseMsg)
	if result.err != nil || calls.Load()-before != 1 || !m.session.GoalPaused() {
		t.Fatalf("settings changed live budget or informational command reset: %v", result.err)
	}
}
