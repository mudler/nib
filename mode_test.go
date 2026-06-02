package main

import "testing"

func TestSelectMode(t *testing.T) {
	cases := []struct {
		name string
		in   modeInputs
		want runMode
	}{
		{"bare defaults to tui", modeInputs{}, modeTUI},
		{"cli flag", modeInputs{cli: true}, modeCLI},
		{"tui flag", modeInputs{tui: true}, modeTUI},
		{"height opts into inline", modeInputs{height: "50%"}, modeInline},
		{"tmux flag inline", modeInputs{tmux: true}, modeInline},
		{"cli wins over height", modeInputs{cli: true, height: "50%"}, modeCLI},
		{"bare in tmux still direct tui", modeInputs{inTmux: true}, modeTUI},
	}
	for _, c := range cases {
		if got := selectMode(c.in); got != c.want {
			t.Errorf("%s: selectMode(%+v) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}
