package chat

import "testing"

func TestResolveAgentModel(t *testing.T) {
	main := "qwen-main"
	configured := map[string]bool{"qwen-big": true, "qwen-small": true}

	cases := []struct {
		name      string
		requested string
		want      string
	}{
		{"empty falls back to main", "", main},
		{"main is honored", main, main},
		{"configured agent model is honored", "qwen-big", "qwen-big"},
		{"another configured model is honored", "qwen-small", "qwen-small"},
		{"invented model falls back to main", "sonar", main},
		{"unknown model falls back to main", "gpt-4", main},
	}
	for _, c := range cases {
		if got := resolveAgentModel(c.requested, main, configured); got != c.want {
			t.Errorf("%s: resolveAgentModel(%q) = %q, want %q", c.name, c.requested, got, c.want)
		}
	}
}
