package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
)

// TestCompactionStatusClearedAfterCompactNotice reproduces the bug where the
// "Compacting conversation…" status (set via OnStatus during auto-compaction)
// persists in the thinking area after compaction completes.
//
// Flow:
//  1. A turn is in progress (loading=true), so m.status shows "Thinking…".
//  2. Auto-compaction fires: OnStatus("Compacting conversation…") arrives as a
//     statusMsg, setting m.status.
//  3. OnCompactDone fires: a compactNoticeMsg arrives, appending the notice.
//
// After step 3, if the turn is still in progress (loading), m.status must NOT
// still say "Compacting conversation…" — it should be back to "Thinking…" (or
// empty). Otherwise the stale "Compacting conversation…" stays on screen for
// the rest of the turn, because tool-call and reasoning events never update
// m.status.
func TestCompactionStatusClearedAfterCompactNotice(t *testing.T) {
	m := newTestModel(Model{
		viewport: viewport.New(80, 20),
		width:    80,
		loading:  true,
	})
	m.startThinking()

	// Simulate OnStatus("Compacting conversation…") arriving mid-turn.
	next, _ := m.Update(statusMsg("Compacting conversation…"))
	m = next.(Model)
	if m.status != "Compacting conversation…" {
		t.Fatalf("after statusMsg, m.status = %q, want %q", m.status, "Compacting conversation…")
	}

	// Simulate OnCompactDone firing — the compactNoticeMsg arrives.
	next, _ = m.Update(compactNoticeMsg([2]int{50000, 12000}))
	m = next.(Model)

	// The turn is still in progress (loading=true), so the status must not
	// still say "Compacting conversation…". It should be back to "Thinking…"
	// so the thinking indicator is shown correctly for the rest of the turn.
	if m.loading && m.status == "Compacting conversation…" {
		t.Fatalf("compaction status is stuck: m.status = %q after compactNoticeMsg while still loading", m.status)
	}
}

// TestCompactionStatusClearedAfterCompactNoticeEndOfTurn covers the
// end-of-turn compaction path: compaction fires after the last LLM response,
// so by the time compactNoticeMsg arrives the turn may already be ending. The
// status must not persist.
func TestCompactionStatusClearedAfterCompactNoticeEndOfTurn(t *testing.T) {
	m := newTestModel(Model{
		viewport: viewport.New(80, 20),
		width:    80,
		loading:  true,
	})
	m.startThinking()

	// Status arrives: compaction is happening.
	next, _ := m.Update(statusMsg("Compacting conversation…"))
	m = next.(Model)

	// The compactNoticeMsg arrives (OnCompactDone).
	next, _ = m.Update(compactNoticeMsg([2]int{50000, 12000}))
	m = next.(Model)

	// Even if the turn ends right after (responseMsg clears m.status), the
	// compactNoticeMsg itself should not leave the compaction status behind.
	// Verify by checking viewState — the rendered status must not be the
	// stale compaction string when the turn is still loading.
	vs := m.viewState()
	if m.loading && vs.Status == "Compacting conversation…" {
		t.Fatalf("viewState still shows stale compaction status %q after compactNoticeMsg", vs.Status)
	}
}
