package tui

import (
	"context"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/config"
	"github.com/mudler/nib/types"
	"strings"
	"testing"
)

func TestGoalGuardSettingsAreStartupOnly(t *testing.T) {
	m, path := newSettingsTestModel(t)
	session, err := chat.NewSession(context.Background(), types.Config{BaseDir: t.TempDir(), InitialGoal: "keep", InitialGoalPaused: true}, chat.Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	m.session = session
	for key, value := range map[string]string{"goal.max_reprompts": "-1", "goal.reprompt_window": "30s"} {
		s, err := config.LookupSetting(key)
		if err != nil {
			t.Fatal(err)
		}
		before := s.Format(m.cfg)
		m.dispatchResolved("/settings " + key + " " + value)
		if msg := lastMessage(t, m); msg.Role == "error" || !strings.Contains(msg.Content, "next start") {
			t.Fatalf("notice: %+v", msg)
		}
		if s.Format(m.cfg) != before || isLiveSetting(key) {
			t.Fatal("startup setting applied live")
		}
		saved, _, err := config.FileSettings(path)
		if err != nil {
			t.Fatal(err)
		}
		if s.Format(saved) != value {
			t.Fatal("setting not saved")
		}
	}
	if session.Goal() != "keep" || !session.GoalPaused() {
		t.Fatal("settings changed paused goal")
	}
	if !isLiveSetting("goal.check_in_delays") {
		t.Fatal("supervision setting no longer live")
	}
}
