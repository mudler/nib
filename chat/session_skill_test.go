package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/mudler/wiz/types"
)

func TestLoadSkill(t *testing.T) {
	s, err := NewSession(context.Background(), types.Config{
		Skills: []types.Skill{{Name: "git-commit", Description: "commit helper", Instructions: "SKILL BODY HERE"}},
	}, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	before := s.systemPrompt

	notice, err := s.LoadSkill("git-commit")
	if err != nil {
		t.Fatalf("LoadSkill: %v", err)
	}
	if !strings.Contains(notice, "git-commit") {
		t.Fatalf("notice should name the skill: %q", notice)
	}
	if !strings.Contains(s.systemPrompt, "SKILL BODY HERE") {
		t.Fatalf("skill body not appended to system prompt:\n%s", s.systemPrompt)
	}
	if len(s.systemPrompt) <= len(before) {
		t.Fatal("system prompt did not grow")
	}

	if _, err := s.LoadSkill("nope"); err == nil {
		t.Fatal("expected error for unknown skill")
	}
}
