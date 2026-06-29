package chat

import "testing"

func TestReadOnlyCommandsMatch(t *testing.T) {
	c := newReadOnlyCommands(nil)
	cases := []struct {
		words []string
		want  bool
	}{
		{[]string{"ls"}, true},
		{[]string{"ls", "-la"}, true},
		{[]string{"cat", "f"}, true},
		{[]string{"rm", "f"}, false},
		{[]string{"git", "status"}, true},
		{[]string{"git", "push"}, false},
		{[]string{"git"}, false}, // bare git has no read-only meaning here
		{[]string{"go", "build"}, false},
		{[]string{"go", "list", "./..."}, true},
		{[]string{"docker", "ps"}, true},
		{[]string{"docker", "run", "x"}, false},
		{[]string{"unknowncmd"}, false},
	}
	for _, tc := range cases {
		if got := c.match(tc.words); got != tc.want {
			t.Errorf("match(%v) = %v, want %v", tc.words, got, tc.want)
		}
	}
}

func TestReadOnlyCommandsExtra(t *testing.T) {
	c := newReadOnlyCommands([]string{"terraform plan", "mycli"})
	if !c.match([]string{"terraform", "plan"}) {
		t.Error("configured pair 'terraform plan' should match")
	}
	if c.match([]string{"terraform", "apply"}) {
		t.Error("'terraform apply' must not match")
	}
	if !c.match([]string{"mycli", "--whatever"}) {
		t.Error("configured whole-command 'mycli' should match at any args")
	}
}
