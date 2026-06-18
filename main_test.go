package main

import "testing"

func TestEnvTrue(t *testing.T) {
	truthy := []string{"1", "true", "TRUE", "yes", "on", "y", " 1 ", "anything"}
	for _, v := range truthy {
		if !envTrue(v) {
			t.Errorf("envTrue(%q) = false, want true", v)
		}
	}
	falsy := []string{"", "  ", "0", "false", "FALSE", "no", "off", " off "}
	for _, v := range falsy {
		if envTrue(v) {
			t.Errorf("envTrue(%q) = true, want false", v)
		}
	}
}
