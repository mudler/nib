package tui

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func replaceDraft(t *testing.T, d *inputDraft, a, b int, s string, paste bool) int {
	t.Helper()
	cursor, err := d.Replace(a, b, s, paste)
	if err != nil {
		t.Fatal(err)
	}
	return cursor
}
func TestDraftThresholds(t *testing.T) {
	for _, tc := range []struct {
		s      string
		folded bool
	}{
		{strings.Repeat("界", 1000), false}, {strings.Repeat("界", 1001), true},
		{"1\n2\n3\n4\n5", false}, {"1\n2\n3\n4\n", false}, {"1\n2\n3\n4\n5\n", true}, {"1\r\n2\r\n3\r\n4\r\n5\r\n", true},
	} {
		var d inputDraft
		replaceDraft(t, &d, 0, 0, tc.s, true)
		p := d.Projection()
		if (p.Spans[0].Kind == draftPaste) != tc.folded {
			t.Fatalf("threshold for %q", tc.s)
		}
		if d.Expanded() != tc.s {
			t.Fatal("lossy")
		}
		if tc.folded && len(p.Text) > 100 {
			t.Fatal("unbounded preview")
		}
	}
}
func TestDraftOrderedOpaqueEdits(t *testing.T) {
	var d inputDraft
	literal := "  界 [paste #1: 6 lines, 10 bytes]\n"
	n := replaceDraft(t, &d, 0, 0, literal, false)
	payload := " a\nβ\n\n c\n d\n "
	n = replaceDraft(t, &d, n, n, payload, true)
	n = replaceDraft(t, &d, n, n, " middle ", false)
	replaceDraft(t, &d, n, n, payload, true)
	p := d.Projection()
	first, second := p.Spans[1], p.Spans[3]
	if first.ID == second.ID {
		t.Fatal("duplicate IDs")
	}
	if d.Expanded() != literal+payload+" middle "+payload {
		t.Fatal("ordering/whitespace")
	}
	if got, ok := d.Preview(first.ID); !ok || got != payload {
		t.Fatal("preview")
	}
	before := d.Expanded()
	if _, err := d.Replace(first.Start+1, first.Start+1, "x", false); !errors.Is(err, errDraftOpaque) || d.Expanded() != before {
		t.Fatal("interior insert must reject")
	}
	if err := d.EditPaste(first.ID, " edited \n"); err != nil {
		t.Fatal(err)
	}
	if d.Projection().Spans[1].ID != first.ID {
		t.Fatal("edit changed identity")
	}
	if err := d.RemovePaste(second.ID); err != nil {
		t.Fatal(err)
	}
	if d.Expanded() != literal+" edited \n middle " {
		t.Fatal(d.Expanded())
	}
}
func TestDraftAtomicSelectionAndMapping(t *testing.T) {
	var d inputDraft
	replaceDraft(t, &d, 0, 0, "界\né", false)
	p := d.Projection()
	if got, err := p.Offset(1, 2); err != nil || got != 4 {
		t.Fatalf("logical cursor %d %v", got, err)
	}
	n := utf8.RuneCountInString(p.Text)
	replaceDraft(t, &d, n, n, strings.Repeat("🙂", 1001), true)
	p = d.Projection()
	s := p.Spans[1]
	if p.ExpandedOffset(s.Start+1, false) != s.ExpandedStart || p.ExpandedOffset(s.Start+1, true) != s.ExpandedEnd {
		t.Fatal("display affinity")
	}
	if p.DisplayOffset(s.ExpandedStart+1, false) != s.Start || p.DisplayOffset(s.ExpandedStart+1, true) != s.End {
		t.Fatal("expanded affinity")
	}
	cursor := replaceDraft(t, &d, s.End-1, s.End, "", false)
	if cursor != s.Start || d.Expanded() != "界\né" {
		t.Fatal("backspace was not atomic")
	}
	replaceDraft(t, &d, cursor, cursor, strings.Repeat("z", 1001), true)
	p = d.Projection()
	s = p.Spans[1]
	replaceDraft(t, &d, s.Start, s.Start+1, "!", false)
	if d.Expanded() != "界\né!" {
		t.Fatal("forward delete was not atomic")
	}
}
func TestDraftLimitTransactionalAndSnapshots(t *testing.T) {
	var d inputDraft
	replaceDraft(t, &d, 0, 0, strings.Repeat("界", 349525)+"x", true) // exactly 1 MiB
	snap := d.Snapshot()
	encoded, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	var decoded draftSnapshot
	if err = json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	var restored inputDraft
	if err = restored.Restore(decoded); err != nil {
		t.Fatal(err)
	}
	if restored.Expanded() != d.Expanded() {
		t.Fatal("round trip")
	}
	n := utf8.RuneCountInString(d.Projection().Text)
	if _, err = d.Replace(n, n, "x", false); !errors.Is(err, errDraftTooLarge) {
		t.Fatal(err)
	}
	id := snap.Segments[0].ID
	if err = d.EditPaste(id, strings.Repeat("x", draftMaxBytes+1)); !errors.Is(err, errDraftTooLarge) {
		t.Fatal(err)
	}
	if d.Expanded() != restored.Expanded() {
		t.Fatal("rejection changed draft")
	}
	if err = d.EditPaste(id, "small"); err != nil {
		t.Fatal(err)
	}
	if restored.Expanded() == d.Expanded() || snap.Segments[0].Text == "small" {
		t.Fatal("snapshot alias")
	}
	decoded.Segments[0].Text = "mutated"
	if restored.Expanded() == "mutated" {
		t.Fatal("restore alias")
	}
	if _, err = d.Replace(0, 0, string([]byte{0xff}), false); !errors.Is(err, errDraftUTF8) {
		t.Fatal(err)
	}
}
func TestDraftSnapshotRejectsMissingPayloadAndDuplicateIDs(t *testing.T) {
	var d inputDraft
	replaceDraft(t, &d, 0, 0, "keep", false)
	for _, s := range []draftSnapshot{
		{Segments: []draftSegmentSnapshot{{ID: 1, Kind: draftPaste}}},
		{Segments: []draftSegmentSnapshot{{ID: 1, Kind: draftText, Text: "a"}, {ID: 1, Kind: draftText, Text: "b"}}},
		{Segments: []draftSegmentSnapshot{{ID: 1, Kind: "unknown", Text: "x"}}},
	} {
		if err := d.Restore(s); err == nil || d.Expanded() != "keep" {
			t.Fatal("invalid snapshot accepted")
		}
	}
}

func TestDraftTextSplitAndCrossBlockReplacement(t *testing.T) {
	var d inputDraft
	replaceDraft(t, &d, 0, 0, " α🙂ω ", false)
	replaceDraft(t, &d, 2, 3, "界", false)
	if d.Expanded() != " α界ω " {
		t.Fatal(d.Expanded())
	}
	replaceDraft(t, &d, 3, 3, strings.Repeat("x", 1001), true)
	p := d.Projection()
	var block draftSpan
	for _, s := range p.Spans {
		if s.Kind == draftPaste {
			block = s
		}
	}
	replaceDraft(t, &d, 1, block.End+1, "new", false)
	if d.Expanded() != " new " {
		t.Fatal(d.Expanded())
	}
	snap := d.Snapshot()
	seen := map[uint64]bool{}
	for _, s := range snap.Segments {
		if seen[s.ID] {
			t.Fatal("split duplicated ID")
		}
		seen[s.ID] = true
	}
	for _, r := range [][2]int{{-1, 0}, {2, 1}, {0, 100}} {
		if _, err := d.Replace(r[0], r[1], "bad", false); err == nil {
			t.Fatal("accepted invalid range")
		}
	}
	if d.Expanded() != " new " {
		t.Fatal("range error mutated draft")
	}
	p = d.Projection()
	p.Spans[0].ID = 999
	if d.Projection().Spans[0].ID == 999 {
		t.Fatal("projection aliases model")
	}
}

func TestDraftRestoredIDsAndExactLiteralLabel(t *testing.T) {
	var d inputDraft
	replaceDraft(t, &d, 0, 0, strings.Repeat("x", 1001), true)
	p := d.Projection()
	label := p.Text
	id := p.Spans[0].ID
	n := replaceDraft(t, &d, p.Spans[0].End, p.Spans[0].End, label, false)
	replaceDraft(t, &d, n, n, strings.Repeat("x", 1001), true)
	var restored inputDraft
	if err := restored.Restore(d.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err := restored.RemovePaste(id); err != nil {
		t.Fatal(err)
	}
	if restored.Expanded() != label+strings.Repeat("x", 1001) {
		t.Fatal("literal label expanded")
	}
	oldNext := restored.Snapshot().NextID
	replaceDraft(t, &restored, 0, 0, strings.Repeat("y", 1001), true)
	if restored.Projection().Spans[0].ID < oldNext {
		t.Fatal("restored counter reused ID")
	}
}
