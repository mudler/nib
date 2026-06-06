package tui

import (
	"strings"
	"testing"
)

func TestQueueMutators(t *testing.T) {
	m := Model{queue: []string{"a", "b", "c"}, queueSel: 0}

	m.queueMoveSel(1)
	if m.queueSel != 1 {
		t.Fatalf("queueMoveSel(1): sel = %d, want 1", m.queueSel)
	}
	m.queueMoveSel(-5) // clamp at 0
	if m.queueSel != 0 {
		t.Fatalf("queueMoveSel clamp low: sel = %d, want 0", m.queueSel)
	}
	m.queueSel = 2
	m.queueMoveSel(5) // clamp at last
	if m.queueSel != 2 {
		t.Fatalf("queueMoveSel clamp high: sel = %d, want 2", m.queueSel)
	}

	m.queueSel = 1
	removed := m.queueDeleteSel()
	if removed != "b" || strings.Join(m.queue, ",") != "a,c" {
		t.Fatalf("queueDeleteSel: removed=%q queue=%v", removed, m.queue)
	}
	if m.queueSel != 1 { // c shifted into index 1
		t.Fatalf("queueDeleteSel sel = %d, want 1", m.queueSel)
	}

	m.queueSel = 1 // now points at "c"
	m.queueDeleteSel()
	if m.queueSel != 0 || strings.Join(m.queue, ",") != "a" {
		t.Fatalf("queueDeleteSel last: sel=%d queue=%v", m.queueSel, m.queue)
	}
}

func TestRenderQueueContent(t *testing.T) {
	if renderQueue(nil, 0, 80) != "" {
		t.Fatal("empty queue should render nothing")
	}
	out := renderQueue([]string{"first item", "second item"}, 1, 80)
	if !strings.Contains(out, "first item") || !strings.Contains(out, "second item") {
		t.Fatalf("renderQueue missing entries: %q", out)
	}
	if containsEmoji(out) {
		t.Fatalf("renderQueue must not contain emoji: %q", out)
	}
}
