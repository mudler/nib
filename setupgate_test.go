package main

import "testing"

func TestDecideSetup(t *testing.T) {
	cases := []struct {
		name            string
		modelConfigured bool
		forced          bool
		isTTY           bool
		want            setupDecision
	}{
		{"configured, no flag", true, false, true, setupSkip},
		{"missing model, tty", false, false, true, setupRun},
		{"missing model, no tty", false, false, false, setupAbort},
		{"forced, configured, tty", true, true, true, setupRun},
		{"forced, no tty", true, true, false, setupAbort},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decideSetup(c.modelConfigured, c.forced, c.isTTY)
			if got != c.want {
				t.Errorf("decideSetup(%v,%v,%v) = %d, want %d", c.modelConfigured, c.forced, c.isTTY, got, c.want)
			}
		})
	}
}
