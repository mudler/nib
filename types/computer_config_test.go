package types

import "testing"

func TestComputerConfigDefaultDisabled(t *testing.T) {
	var c Config
	if c.Computer.Enabled {
		t.Fatalf("computer control must default disabled")
	}
}
