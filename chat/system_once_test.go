package chat

import (
	"testing"

	"github.com/mudler/cogito"
)

func TestFragmentHasSystemContent(t *testing.T) {
	sys := "You are Dante."
	// Fresh fragment: no system yet → false (SendMessage should add it once).
	f := cogito.NewEmptyFragment()
	if fragmentHasSystemContent(f, sys) {
		t.Fatal("empty fragment should not report the system prompt present")
	}
	f = f.AddMessage("system", sys)
	f = f.AddMessage("user", "hi")
	// Now present → true (SendMessage must NOT re-add it, avoiding duplication).
	if !fragmentHasSystemContent(f, sys) {
		t.Fatal("fragment with the system prompt should report present")
	}
	// A different system content is not a match.
	if fragmentHasSystemContent(f, "different prompt") {
		t.Fatal("different content must not match")
	}
}
