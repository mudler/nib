package tui

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const draftMaxBytes = 1 << 20

type draftKind string

const (
	draftText  draftKind = "text"
	draftPaste draftKind = "paste"
)

var (
	errDraftTooLarge = errors.New("draft exceeds 1 MiB UTF-8 limit (not a model context limit)")
	errDraftUTF8     = errors.New("draft must contain valid UTF-8")
	errDraftOpaque   = errors.New("cannot insert inside a paste block; preview or edit the block instead")
	errDraftRange    = errors.New("invalid draft range")
	errDraftSnapshot = errors.New("invalid draft snapshot")
	errDraftPaste    = errors.New("paste block not found")
)

type draftSegmentSnapshot struct {
	ID   uint64    `json:"id"`
	Kind draftKind `json:"kind"`
	Text string    `json:"text"`
}

// draftSnapshot owns its slice. Strings are immutable. Store these, not display
// labels, for history, queues and draft recovery. Restore validates untrusted data.
type draftSnapshot struct {
	Segments []draftSegmentSnapshot `json:"segments"`
	NextID   uint64                 `json:"next_id"`
}
type draftSegment struct {
	draftSegmentSnapshot
	runes        int
	display      string
	displayRunes int
}

// inputDraft's zero value is ready for use. Do not copy a live draft to create
// an independent draft; use Snapshot/Restore. IDs are local to a draft lineage.
// No method interprets user text as a placeholder or expands labels by matching.
type inputDraft struct {
	segments   []draftSegment
	nextID     uint64
	bytes      int
	projection draftProjection
}
type draftSpan struct {
	ID                         uint64
	Kind                       draftKind
	Start, End                 int // half-open display rune offsets, not terminal cells or bytes
	ExpandedStart, ExpandedEnd int // half-open payload rune offsets
}
type draftProjection struct {
	Text  string
	Spans []draftSpan
}

func makeDraftSegment(id uint64, kind draftKind, text string) draftSegment {
	s := draftSegment{draftSegmentSnapshot: draftSegmentSnapshot{id, kind, text}, runes: utf8.RuneCountInString(text), display: text}
	if kind == draftPaste {
		s.display = fmt.Sprintf("[paste #%d: %d lines, %d bytes]", id, strings.Count(text, "\n")+1, len(text))
	}
	// Keep inline source rune-for-rune addressable, but never feed control
	// characters to textarea's lossy sanitizer (tabs expand, CR becomes LF).
	// Visible control pictures are projection only; the original bytes remain
	// in Text, and editing one picture edits precisely that source rune.
	if kind == draftText {
		s.display = strings.Map(func(r rune) rune {
			if r == '\n' {
				return r
			}
			if r < 32 {
				return '\u2400' + r
			}
			if r == 127 {
				return '\u2421'
			}
			if unicode.IsControl(r) {
				return '\ufffd'
			}
			return r
		}, text)
	}
	s.displayRunes = utf8.RuneCountInString(s.display)
	return s
}
func validateDraftText(s string) error {
	if len(s) > draftMaxBytes {
		return errDraftTooLarge
	}
	if !utf8.ValidString(s) {
		return errDraftUTF8
	}
	return nil
}
func (d *inputDraft) rebuild() {
	var b strings.Builder
	p := draftProjection{Spans: make([]draftSpan, 0, len(d.segments))}
	pos, expanded := 0, 0
	d.bytes = 0
	for _, s := range d.segments {
		b.WriteString(s.display)
		p.Spans = append(p.Spans, draftSpan{s.ID, s.Kind, pos, pos + s.displayRunes, expanded, expanded + s.runes})
		pos += s.displayRunes
		expanded += s.runes
		d.bytes += len(s.Text)
	}
	p.Text = b.String()
	d.projection = p
}

// Projection is cached on mutation and never scans hidden payload. The returned
// spans are a defensive copy; the text is immutable and bounded per paste block.
func (d *inputDraft) Projection() draftProjection {
	p := d.projection
	p.Spans = append([]draftSpan(nil), p.Spans...)
	return p
}

// Expanded is the authoritative send text. Call on submission, not each frame.
func (d *inputDraft) Expanded() string {
	var b strings.Builder
	b.Grow(d.bytes)
	for _, s := range d.segments {
		b.WriteString(s.Text)
	}
	return b.String()
}
func (d *inputDraft) Snapshot() draftSnapshot {
	s := draftSnapshot{NextID: d.nextID, Segments: make([]draftSegmentSnapshot, len(d.segments))}
	for i, seg := range d.segments {
		s.Segments[i] = seg.draftSegmentSnapshot
	}
	return s
}
func (d *inputDraft) Restore(s draftSnapshot) error {
	candidate := inputDraft{nextID: s.NextID}
	seen := map[uint64]bool{}
	for _, seg := range s.Segments {
		if seg.ID == 0 || seg.ID == ^uint64(0) || seen[seg.ID] || seg.Text == "" || (seg.Kind != draftText && seg.Kind != draftPaste) {
			return errDraftSnapshot
		}
		if err := validateDraftText(seg.Text); err != nil {
			return err
		}
		candidate.bytes += len(seg.Text)
		if candidate.bytes > draftMaxBytes {
			return errDraftTooLarge
		}
		seen[seg.ID] = true
		if candidate.nextID <= seg.ID {
			candidate.nextID = seg.ID + 1
		}
		candidate.segments = append(candidate.segments, makeDraftSegment(seg.ID, seg.Kind, seg.Text))
	}
	if candidate.nextID == ^uint64(0) {
		return errDraftSnapshot
	}
	candidate.rebuild()
	*d = candidate
	return nil
}

// Replace applies a display-rune selection. Nonempty ranges touching any part
// of a paste expand to its entire span (backspace/delete are one-rune ranges).
// Insertion inside a block rejects rather than guessing a boundary. All errors
// retain the old draft and ID counter. The returned cursor is after insertion.
// paste=true folds only this inserted payload at >=6 LF-delimited logical lines
// (including a trailing empty line) or >1000 code points. CRLF bytes stay intact.
func (d *inputDraft) Replace(start, end int, text string, paste bool) (int, error) {
	if err := validateDraftText(text); err != nil {
		return start, err
	}
	p := d.projection
	length := 0
	if len(p.Spans) > 0 {
		length = p.Spans[len(p.Spans)-1].End
	}
	if start < 0 || end < start || end > length {
		return start, errDraftRange
	}
	for _, s := range p.Spans {
		if s.Kind != draftPaste {
			continue
		}
		if start == end && start > s.Start && start < s.End {
			return start, errDraftOpaque
		}
		if start < end && start < s.End && end > s.Start {
			start = min(start, s.Start)
			end = max(end, s.End)
		}
	}
	next := d.nextID
	if next == 0 {
		next = 1
	}
	alloc := func() uint64 { id := next; next++; return id }
	var left, right []draftSegment
	removed := 0
	for i, s := range d.segments {
		span := p.Spans[i]
		if span.End <= start {
			left = append(left, s)
			continue
		}
		if span.Start >= end {
			right = append(right, s)
			continue
		}
		removed += len(s.Text)
		if s.Kind == draftText {
			r := []rune(s.Text)
			if start > span.Start {
				part := makeDraftSegment(s.ID, draftText, string(r[:start-span.Start]))
				left = append(left, part)
				removed -= len(part.Text)
			}
			if end < span.End {
				id := s.ID
				if start > span.Start {
					id = alloc()
				}
				part := makeDraftSegment(id, draftText, string(r[end-span.Start:]))
				right = append(right, part)
				removed -= len(part.Text)
			}
		}
	}
	if d.bytes-removed+len(text) > draftMaxBytes {
		return start, errDraftTooLarge
	}
	cursor := start
	if text != "" {
		kind := draftText
		if paste && (strings.Count(text, "\n") >= 5 || utf8.RuneCountInString(text) > 1000) {
			kind = draftPaste
		}
		s := makeDraftSegment(alloc(), kind, text)
		left = append(left, s)
		cursor += s.displayRunes
	}
	if next == 0 || next == ^uint64(0) {
		return start, errDraftSnapshot
	}
	d.segments = append(left, right...)
	d.nextID = next
	d.rebuild()
	return cursor, nil
}
func (d *inputDraft) Preview(id uint64) (string, bool) {
	for _, s := range d.segments {
		if s.ID == id && s.Kind == draftPaste {
			return s.Text, true
		}
	}
	return "", false
}

// EditPaste preserves identity and opacity even if the replacement is small.
// An empty edit removes the block. No preview or edit operation submits text.
func (d *inputDraft) EditPaste(id uint64, text string) error {
	if err := validateDraftText(text); err != nil {
		return err
	}
	for i, s := range d.segments {
		if s.ID != id || s.Kind != draftPaste {
			continue
		}
		if d.bytes-len(s.Text)+len(text) > draftMaxBytes {
			return errDraftTooLarge
		}
		if text == "" {
			d.segments = append(d.segments[:i], d.segments[i+1:]...)
		} else {
			d.segments[i] = makeDraftSegment(id, draftPaste, text)
		}
		d.rebuild()
		return nil
	}
	return errDraftPaste
}
func (d *inputDraft) RemovePaste(id uint64) error { return d.EditPaste(id, "") }

// ExpandedOffset/DisplayOffset clamp to the projection. Interior opaque
// positions snap to the leading edge, or trailing edge when after is true.
func (p draftProjection) ExpandedOffset(offset int, after bool) int {
	return p.mapOffset(offset, after, false)
}
func (p draftProjection) DisplayOffset(offset int, after bool) int {
	return p.mapOffset(offset, after, true)
}
func (p draftProjection) mapOffset(offset int, after, reverse bool) int {
	offset = max(0, offset)
	last := 0
	for _, s := range p.Spans {
		a, b, c, e := s.Start, s.End, s.ExpandedStart, s.ExpandedEnd
		if reverse {
			a, b, c, e = c, e, a, b
		}
		last = e
		if offset <= a {
			return c
		}
		if offset >= b {
			continue
		}
		if s.Kind == draftPaste {
			if after {
				return e
			}
			return c
		}
		return c + offset - a
	}
	return last
}

// Offset converts a logical line and rune column to a display offset. For
// bubbles textarea v0.21 use Line() and LineInfo().StartColumn+ColumnOffset,
// NOT CharOffset (terminal cell width). Soft wraps do not introduce newlines.
func (p draftProjection) Offset(line, column int) (int, error) {
	if line < 0 || column < 0 {
		return 0, errDraftRange
	}
	row, col, pos := 0, 0, 0
	for _, r := range p.Text {
		if row == line && col == column {
			return pos, nil
		}
		if r == '\n' {
			if row == line {
				return 0, errDraftRange
			}
			row++
			col = 0
		} else {
			col++
		}
		pos++
	}
	if row == line && col == column {
		return pos, nil
	}
	return 0, errDraftRange
}
