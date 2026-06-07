package chat

import (
	"context"
	"testing"

	"github.com/mudler/nib/types"
)

func TestGoalAccessors(t *testing.T) {
	s, err := NewSession(context.Background(), types.Config{}, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	if s.Goal() != "" {
		t.Fatalf("new session goal = %q, want empty", s.Goal())
	}
	s.SetGoal("make tests pass")
	if s.Goal() != "make tests pass" {
		t.Fatalf("Goal() = %q after SetGoal", s.Goal())
	}
	s.SetGoal("replace it")
	if s.Goal() != "replace it" {
		t.Fatalf("Goal() = %q, want replaced", s.Goal())
	}
	s.ClearGoal()
	if s.Goal() != "" {
		t.Fatalf("Goal() = %q after ClearGoal, want empty", s.Goal())
	}
}
