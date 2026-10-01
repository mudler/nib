package tui

import (
	"context"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/tui/render"

	"github.com/mudler/nib/theme"
)

// The streamed reply is revealed at a steady pace instead of one jump per
// provider chunk. Providers deliver content in bursts (a few tokens, a
// pause, a whole line), so drawing each burst as it lands looks jerky. The
// transcript keeps the full text (reconciliation in responseMsg/parkMsg is
// unchanged); only the render of the streaming tail is cut at
// streamShown, and a tick advances that cut toward the end.
const (
	// streamRevealInterval is one reveal frame, about 33 fps. A faster rate
	// adds full-transcript renders without a visible gain in a terminal.
	streamRevealInterval = 30 * time.Millisecond
	// streamRevealDrainFrames is how many frames a backlog takes to drain:
	// each frame reveals 1/N of what is still hidden, so a large burst
	// shows fast and the end of it slows down (an ease-out). With 8 frames
	// the text stays at most about 250ms behind the model.
	streamRevealDrainFrames = 8
)

// animTickMsg advances the animations by one frame: the reveal of a reply,
// and the fade-in of new entries.
type animTickMsg struct{}

// animClock sends animTickMsg every streamRevealInterval while a frame has
// something moving, and sleeps otherwise, so an idle TUI does not redraw.
// It runs on its own goroutine and reaches Update through listenAnim, like
// the session callbacks do through their channels, so no handler has to
// return a tick Cmd: updateViewport calls want after each frame.
type animClock struct {
	wanted atomic.Bool
	wake   chan struct{}
	ticks  chan animTickMsg
}

func newAnimClock(ctx context.Context) *animClock {
	c := &animClock{wake: make(chan struct{}, 1), ticks: make(chan animTickMsg)}
	go c.run(ctx)
	return c
}

func (c *animClock) run(ctx context.Context) {
	for {
		select {
		case <-c.wake:
		case <-ctx.Done():
			return
		}
		for c.wanted.Load() {
			select {
			case <-time.After(streamRevealInterval):
			case <-ctx.Done():
				return
			}
			select {
			case c.ticks <- animTickMsg{}:
			case <-ctx.Done():
				return
			}
		}
	}
}

// want records whether the last frame had something moving, and wakes the
// clock when it was asleep. A nil clock (a Model literal in a test) is a
// no-op: tests send animTickMsg themselves.
func (c *animClock) want(moving bool) {
	if c == nil {
		return
	}
	if !moving {
		c.wanted.Store(false)
		return
	}
	if !c.wanted.Swap(true) {
		select {
		case c.wake <- struct{}{}:
		default:
		}
	}
}

// listenAnim waits for the next animation frame.
func (m Model) listenAnim() tea.Cmd {
	if m.anim == nil {
		return nil
	}
	return func() tea.Msg {
		select {
		case t := <-m.anim.ticks:
			return t
		case <-m.ctx.Done():
			return nil
		}
	}
}

// animating reports whether another frame would change the screen.
func (m Model) animating() bool {
	return m.streamBacklog() || m.reasoningBacklog() || m.reasoningArriving() > 0 || m.fading()
}

// advanceAnimation moves the reveal one frame forward, and ends a reveal
// after the turn once all of it is drawn, so the entry goes back to the
// cached final render.
func (m *Model) advanceAnimation() {
	m.reasoningShown = revealCut(m.reasoning, m.reasoningShown)
	if m.reasoningBacklog() {
		m.reasoningShown = advanceReveal(m.reasoning, m.reasoningShown)
	}
	if m.streamBacklog() {
		m.advanceStreamReveal()
	}
	if !m.streamingActive && !m.streamBacklog() {
		m.revealIdx, m.revealText = 0, ""
	}
}

// revealTarget returns the index of the reply being revealed: the streaming
// tail, or a reply whose turn ended before all of it was drawn.
func (m Model) revealTarget() (int, bool) {
	if m.streamingActive && len(m.messages) > 0 {
		return len(m.messages) - 1, true
	}
	i := m.revealIdx - 1
	if i >= 0 && i < len(m.messages) && m.messages[i].Content == m.revealText {
		return i, true
	}
	return -1, false
}

// streamBacklog reports whether the revealed reply has text not yet shown.
func (m Model) streamBacklog() bool {
	i, ok := m.revealTarget()
	return ok && m.streamShown < len(m.messages[i].Content)
}

// revealAfterTurn keeps revealing reply i after its turn ended, so the end of
// a fast stream, or a reply that did not stream at all, does not land at
// once. streamed says the reply was streaming, so the part already drawn
// stays drawn; the final text can differ from what streamed, so the cut is
// moved back onto a rune boundary.
func (m *Model) revealAfterTurn(i int, streamed bool) {
	text := m.messages[i].Content
	if !streamed {
		m.streamShown = 0
		m.streamStart = time.Now()
	}
	if m.streamShown > len(text) {
		m.streamShown = len(text)
	}
	for m.streamShown > 0 && m.streamShown < len(text) && !utf8.RuneStart(text[m.streamShown]) {
		m.streamShown--
	}
	if m.streamShown >= len(text) {
		m.revealIdx, m.revealText = 0, "" // all of it is drawn already
		return
	}
	m.revealIdx, m.revealText = i+1, text
}

// advanceStreamReveal moves streamShown forward by one frame. It counts in
// runes and always stops on a rune boundary, so a multi-byte character is
// never cut in half.
func (m *Model) advanceStreamReveal() {
	i, _ := m.revealTarget()
	text := m.messages[i].Content
	m.streamShown = advanceReveal(text, m.streamShown)
}

// revealCut clamps replacement text to a valid rune boundary.
func revealCut(text string, shown int) int {
	if shown > len(text) {
		shown = len(text)
	}
	for shown > 0 && shown < len(text) && !utf8.RuneStart(text[shown]) {
		shown--
	}
	return shown
}

// advanceReveal is the shared ease-out for both live text lanes.
func advanceReveal(text string, shown int) int {
	shown = revealCut(text, shown)
	n := utf8.RuneCountInString(text[shown:])
	step := (n + streamRevealDrainFrames - 1) / streamRevealDrainFrames
	for j := 0; j < step && shown < len(text); j++ {
		_, size := utf8.DecodeRuneInString(text[shown:])
		shown += size
	}
	return shown
}

// fading reports whether an entry near the end of the transcript is still
// fading in. Only the last few can be: entries arrive at the end.
func (m Model) fading() bool {
	for i := len(m.messages) - 1; i >= 0 && i >= len(m.messages)-8; i-- {
		if m.arriving(m.messages[i]) > 0 {
			return true
		}
	}
	return false
}

// arriving is how far msg still is from its full ink: 1 when it has just
// arrived, 0 once theme.FadeDuration has passed.
func (m Model) arriving(msg ChatMessage) float64 {
	if msg.arrived.IsZero() {
		return 0
	}
	age := time.Since(msg.arrived)
	if age >= theme.FadeDuration {
		return 0
	}
	return 1 - float64(age)/float64(theme.FadeDuration)
}

// visibleStreamContent is the part of the revealed reply to draw this frame.
func (m Model) visibleStreamContent(content string) string {
	if m.streamShown >= len(content) {
		return content
	}
	return content[:revealCut(content, m.streamShown)]
}

// renderStreaming renders the visible part of the streaming reply so it looks
// like the final glamour pass will, and nothing jumps when the turn ends.
//
// The settled blocks (everything up to the last block boundary) go through
// the cached renderMarkdown. The open block at the end changes every frame,
// and rendering it on its own would get the gap before it wrong: glamour
// puts no blank line after a heading, and a code block brings its own padded
// one. So the open block is rendered together with the last settled block,
// and the lines of that settled block are dropped from the result. The gap
// is then glamour's own, and each frame renders two blocks, not the reply.
//
// A code block that is still open is closed for the render, so it shows as
// code while it streams rather than as raw text that snaps into a code block
// at the end. The pulsing cursor goes at the end of the last line.
func (m *Model) renderStreaming(visible string, width int) string {
	settled, open, lastStart, inFence := splitSettled(visible)
	if inFence {
		open += "\n" + fenceClose(open)
	}
	var out string
	switch {
	case strings.TrimSpace(open) == "":
		out = m.renderMarkdown(settled, width)
	case strings.TrimSpace(settled) == "":
		out = renderMarkdownWith(m.markdownFor(width), open, width)
	default:
		out = m.renderMarkdown(settled, width) + "\n" + m.renderOpenBlock(settled[lastStart:], open, width)
	}
	return appendCursor(out, theme.StreamCursorAt(time.Since(m.streamStart)), width)
}

// renderOpenBlock renders open as it appears after last in one document,
// without the lines of last itself.
func (m *Model) renderOpenBlock(last, open string, width int) string {
	skip := strings.Count(m.renderMarkdown(last, width), "\n") + 1
	lines := strings.Split(renderMarkdownWith(m.markdownFor(width), last+open, width), "\n")
	if skip >= len(lines) {
		return renderMarkdownWith(m.markdownFor(width), open, width)
	}
	return strings.Join(lines[skip:], "\n")
}

// splitSettled cuts text at its last block boundary: a blank line outside a
// code fence, or the line that closes a fence. settled is complete markdown
// that will not change as more text arrives; open is the block still being
// written. lastStart is where the last block of settled begins. inFence
// reports that open is inside a code fence that has not closed yet.
func splitSettled(text string) (settled, open string, lastStart int, inFence bool) {
	cut, prev := 0, 0
	fence := ""
	pos := 0
	boundary := func(at int) {
		if at > cut && strings.TrimSpace(text[cut:at]) != "" {
			prev = cut
		}
		cut = at
	}
	for pos < len(text) {
		end := strings.IndexByte(text[pos:], '\n')
		if end < 0 {
			break // the last line is incomplete: it can never be a boundary
		}
		line := text[pos : pos+end]
		next := pos + end + 1
		trimmed := strings.TrimSpace(line)
		switch {
		case fence == "" && isFence(trimmed):
			fence = trimmed[:3]
		case fence != "" && strings.HasPrefix(trimmed, fence) && strings.Trim(trimmed, fence[:1]) == "":
			fence = ""
			boundary(next)
		case fence == "" && trimmed == "":
			boundary(next)
		}
		pos = next
	}
	if fence == "" && pos < len(text) && isFence(strings.TrimSpace(text[pos:])) {
		fence = strings.TrimSpace(text[pos:])[:3]
	}
	return text[:cut], text[cut:], prev, fence != ""
}

func isFence(line string) bool {
	return strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~")
}

// fenceClose returns the closing line for the fence that opens block.
func fenceClose(block string) string {
	if strings.HasPrefix(strings.TrimSpace(block), "~~~") {
		return "~~~"
	}
	return "```"
}

// appendCursor puts cursor after the last visible cell of out. glamour pads
// wrapped lines with spaces up to the width, so the padding is cut first. A
// trailing blank line (the padding a code block ends with) is kept, and the
// cursor goes on the last line that has text. When that line is already full
// the cursor starts a new line.
func appendCursor(out, cursor string, width int) string {
	return render.AppendCursor(out, cursor, width)
}
