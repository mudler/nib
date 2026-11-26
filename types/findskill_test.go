package types

import "testing"

func TestFindSkill(t *testing.T) {
	skills := []Skill{{Name: "plain"}, {Name: "sp:brainstorm"}, {Name: "a:dup"}, {Name: "b:dup"}}
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"plain", "plain", true},
		{"sp:brainstorm", "sp:brainstorm", true},
		{"brainstorm", "sp:brainstorm", true},
		{"dup", "", false},
		{"x:brainstorm", "", false},
		{"missing", "", false},
	}
	for _, tt := range tests {
		s, ok := FindSkill(skills, tt.in)
		if ok != tt.ok || s.Name != tt.want {
			t.Errorf("FindSkill(%q) = %q %v, want %q %v", tt.in, s.Name, ok, tt.want, tt.ok)
		}
	}
}
