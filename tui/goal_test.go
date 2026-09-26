package tui

import (
	"context"
	"testing"

	openai "github.com/sashabaranov/go-openai"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

// newGoalModel is a ctrl-c test model with a real (never-run) session.
func newGoalModel(t *testing.T) Model {
	t.Helper()
	return newGoalModelWith(t, types.Config{})
}

// newGoalModelWith is newGoalModel with a session built from cfg.
func newGoalModelWith(t *testing.T, cfg types.Config) Model {
	t.Helper()
	s, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	m := newCtrlCModel()
	m.session = s
	return m
}

// Setting a goal starts a turn at once, so the agent reacts to it instead of
// waiting for another message. Before, the goal sat idle until the user typed
// something, and even then the model did not see the goal text until it
// first stopped and got the reminder.
func TestGoalSetStartsATurn(t *testing.T) {
	m := newGoalModel(t)
	cmd := m.dispatchInput("/goal make all tests pass")
	if cmd == nil || !m.loading {
		t.Fatalf("cmd=%v loading=%v, want /goal to start a turn", cmd, m.loading)
	}
	if got := m.session.Goal(); got != "make all tests pass" {
		t.Fatalf("goal = %q", got)
	}
}

// Resuming a paused goal also starts a turn.
func TestGoalResumeStartsATurn(t *testing.T) {
	m := newGoalModel(t)
	m.session.SetGoal("ship it")
	m.session.PauseGoal()
	if cmd := m.dispatchInput("/goal resume"); cmd == nil || !m.loading {
		t.Fatalf("cmd=%v loading=%v, want /goal resume to start a turn", cmd, m.loading)
	}
	if m.session.GoalPaused() {
		t.Fatal("goal still paused")
	}
}

// Showing or clearing a goal starts nothing.
func TestGoalShowAndClearStartNoTurn(t *testing.T) {
	m := newGoalModel(t)
	m.session.SetGoal("ship it")
	for _, in := range []string{"/goal", "/goal clear", "/goal resume"} {
		if cmd := m.dispatchInput(in); cmd != nil || m.loading {
			t.Fatalf("%s: cmd=%v loading=%v, want no turn", in, cmd, m.loading)
		}
	}
}

// The goal is saved with the session, so a resume can bring it back.
func TestRecordSessionSavesTheGoal(t *testing.T) {
	m := newGoalModelWith(t, types.Config{InitialHistory: []openai.ChatCompletionMessage{{Role: "user", Content: "hi"}}})
	store := chat.NewSessionStore(t.TempDir())
	m.store = store
	m.sessionID = "goal-rec"
	m.session.SetGoal("ship it")
	m.session.PauseGoal()
	m.recordSession()

	rec, err := store.Load("goal-rec")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Goal != "ship it" || !rec.GoalPaused {
		t.Fatalf("saved goal = %q paused = %v, want ship it, paused", rec.Goal, rec.GoalPaused)
	}
}

// /goal clear starts no turn, so it saves at once; otherwise a session
// killed before its next turn would resume with the cleared goal.
func TestGoalClearSavesTheSession(t *testing.T) {
	m := newGoalModelWith(t, types.Config{InitialHistory: []openai.ChatCompletionMessage{{Role: "user", Content: "hi"}}})
	store := chat.NewSessionStore(t.TempDir())
	m.store = store
	m.sessionID = "goal-clear"
	m.session.SetGoal("ship it")
	m.recordSession()

	m.dispatchInput("/goal clear")
	rec, err := store.Load("goal-clear")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Goal != "" {
		t.Fatalf("saved goal = %q after /goal clear, want none", rec.Goal)
	}
}
