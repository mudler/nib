package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/types"
)

// The notice is prose the user reads, so it counts in words as well as digits.
func TestPrunedNoticeWording(t *testing.T) {
	got := prunedNotice(3, 12400)
	for _, want := range []string{"3", "12.4k"} {
		if !strings.Contains(got, want) {
			t.Fatalf("notice %q is missing %q", got, want)
		}
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("notice must stay one line: %q", got)
	}
	if one := prunedNotice(1, 900); strings.Contains(one, "1 stale tool results") {
		t.Fatalf("notice should be singular for one result: %q", one)
	}
	// A stale read is stubbed however small it was, so a pass can free nothing
	// measurable — and HumanTokens renders 0 as the empty string.
	if zero := prunedNotice(1, 0); !strings.Contains(zero, "0") || strings.Contains(zero, "  ") {
		t.Fatalf("a zero saving left a hole in the notice: %q", zero)
	}
}

// A listener nobody starts receives nothing. The prune notice reaches the
// transcript only if sessionReadyMsg issues listenPrune alongside the other
// listeners, and no wiring test would otherwise notice its absence.
func TestSessionReadyStartsThePruneListener(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewModel(ctx, types.Config{}, 40, nil)
	m.pruneChan <- [2]int{2, 4000}

	updated, cmd := m.Update(sessionReadyMsg{})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("sessionReadyMsg started no commands at all")
	}

	// The listeners come back as one batch; run them concurrently, since every
	// listener but ours blocks until its own channel or the context fires.
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("sessionReadyMsg returned %T, want a batch of listeners", cmd())
	}
	msgs := make(chan tea.Msg, len(batch))
	for _, c := range batch {
		go func(c tea.Cmd) { msgs <- c() }(c)
	}
	deadline := time.After(2 * time.Second)
	for range batch {
		select {
		case got := <-msgs:
			if n, ok := got.(pruneNoticeMsg); ok {
				if n != [2]int{2, 4000} {
					t.Fatalf("prune listener delivered %v, want {2 4000}", n)
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for a prune notice")
		}
	}
	t.Fatal("no listener consumed pruneChan: listenPrune is never started")
}
