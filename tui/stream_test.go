package tui

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/chat"
)

// delta wraps one streamed reasoning chunk as the batch-of-one Update expects
// from listenReasoningEvents in the common case.
func delta(text string) reasoningEventsMsg {
	return reasoningEventsMsg{{kind: reasoningEventDelta, text: text}}
}

// boundary wraps one step-boundary (complete) reasoning block the same way.
func boundary(text string) reasoningEventsMsg {
	return reasoningEventsMsg{{kind: reasoningEventBoundary, text: text}}
}

// content wraps one streamed content (assistant-reply) chunk the same way —
// Callbacks.OnStream's "content" kind, carried on the same reasoningChan as
// reasoning deltas/boundaries (see reasoningEventContentDelta's doc).
func content(text string) reasoningEventsMsg {
	return reasoningEventsMsg{{kind: reasoningEventContentDelta, text: text}}
}

// TestReasoningDeltaAccumulatesInOrder feeds a sequence of streaming reasoning
// deltas through Update and asserts they land in m.reasoning concatenated in
// the order they arrived — a delta pipeline that reorders or drops chunks
// would produce a trace nobody could trust.
func TestReasoningDeltaAccumulatesInOrder(t *testing.T) {
	m := Model{
		viewport:  viewport.New(80, 20),
		width:     80,
		loading:   true,
		presenter: testPresenter(),
	}

	deltas := []string{"The", " quick", " brown", " fox", " jumps"}
	var next tea.Model = m
	for _, d := range deltas {
		next, _ = next.(Model).Update(delta(d))
	}

	got := next.(Model).reasoning
	want := strings.Join(deltas, "")
	if got != want {
		t.Fatalf("reasoning = %q, want %q (deltas out of order or dropped)", got, want)
	}
}

// TestReasoningDeltaTailsWhenCollapsed streams enough deltas (one per line) to
// exceed theme.ReasoningMaxLines and asserts the collapsed box still shows the
// newest lines, mirroring TestReasoningCollapsedByDefaultTailsTheTrace but
// arriving via the streaming path instead of a single OnReasoning write.
func TestReasoningDeltaTailsWhenCollapsed(t *testing.T) {
	m := Model{
		viewport:           viewport.New(80, 20),
		width:              80,
		loading:            true,
		presenter:          testPresenter(),
		reasoningCollapsed: true,
	}

	var next tea.Model = m
	for i := 0; i < 20; i++ {
		line := "trace line " + string(rune('a'+i))
		next, _ = next.(Model).Update(delta(line + "\n\n"))
	}

	// Provider bursts update the authoritative buffer immediately; render ticks
	// catch up independently before the collapsed tail is inspected.
	for i := 0; i < 200 && next.(Model).reasoningBacklog(); i++ {
		next, _ = next.(Model).Update(animTickMsg{})
	}
	cur := next.(Model)
	if cur.visibleReasoning() != cur.reasoning || cur.reasoningBacklog() {
		t.Fatal("reveal did not catch up to the complete reasoning buffer")
	}

	out := next.(Model).viewport.View()
	if !strings.Contains(out, "trace line t") {
		t.Error("collapsed box does not show the newest streamed line")
	}
	if strings.Contains(out, "trace line a") {
		t.Error("collapsed box is showing the oldest streamed line; it should tail, not head")
	}
}

// TestReasoningBoundaryResetsStreamAccumulation is the precedence test: a
// step-boundary OnReasoning write is authoritative for the step that just
// ended — it is what the step folds into the transcript as — but it must NOT
// become a prefix that the next step's streamed deltas get appended onto —
// otherwise every step after the first would duplicate the previous step's
// complete text.
func TestReasoningBoundaryResetsStreamAccumulation(t *testing.T) {
	m := Model{
		viewport:  viewport.New(80, 20),
		width:     80,
		loading:   true,
		presenter: testPresenter(),
	}

	var next tea.Model = m
	// Step 1 streams in two chunks.
	next, _ = next.(Model).Update(delta("ab"))
	next, _ = next.(Model).Update(delta("cd"))
	if got := next.(Model).reasoning; got != "abcd" {
		t.Fatalf("mid-step reasoning = %q, want %q", got, "abcd")
	}

	// Step 1 ends: OnReasoning fires with the complete, authoritative block.
	next, _ = next.(Model).Update(boundary("STEP1-COMPLETE"))
	if got := thoughts(next.(Model)); len(got) != 1 || got[0] != "STEP1-COMPLETE" {
		t.Fatalf("post-boundary thoughts = %q, want [STEP1-COMPLETE]", got)
	}
	if got := next.(Model).reasoning; got != "" {
		t.Fatalf("post-boundary live box = %q, want empty (folded into the transcript)", got)
	}
	if !next.(Model).reasoningResetPending {
		t.Fatal("boundary must arm reasoningResetPending for the next step's first delta")
	}

	// Step 2 starts streaming. Its first delta must start a fresh trace, not
	// append onto step 1's authoritative text.
	next, _ = next.(Model).Update(delta("xy"))
	if got := next.(Model).reasoning; got != "xy" {
		t.Fatalf("first delta after boundary = %q, want %q (must not carry over the prior step's text)", got, "xy")
	}

	// Further step-2 deltas keep accumulating normally.
	next, _ = next.(Model).Update(delta("z"))
	if got := next.(Model).reasoning; got != "xyz" {
		t.Fatalf("second delta after boundary = %q, want %q", got, "xyz")
	}
	if got := thoughts(next.(Model)); len(got) != 1 {
		t.Fatalf("thoughts = %q, want step 2 still live, not folded", got)
	}
}

// thoughts returns the folded thought entries in the transcript, in order.
func thoughts(m Model) []string {
	var out []string
	for _, msg := range m.messages {
		if msg.Role == "thought" {
			out = append(out, msg.Content)
		}
	}
	return out
}

// TestConsecutiveBoundariesWithNoDeltaBetween: two step-boundary events with
// no delta in between (e.g. two tool-selection steps in a row that streamed
// no reasoning) fold into two separate thoughts, in order — not a mix of
// both, and not the second overwriting the first. Delivered as one batch, exactly as
// listenReasoningEvents would hand them to Update after draining two queued
// boundary events.
func TestConsecutiveBoundariesWithNoDeltaBetween(t *testing.T) {
	m := Model{
		viewport:  viewport.New(80, 20),
		width:     80,
		loading:   true,
		presenter: testPresenter(),
	}

	batch := reasoningEventsMsg{
		{kind: reasoningEventBoundary, text: "STEP1-COMPLETE"},
		{kind: reasoningEventBoundary, text: "STEP2-COMPLETE"},
	}
	next, _ := m.Update(batch)

	if got := thoughts(next.(Model)); len(got) != 2 || got[0] != "STEP1-COMPLETE" || got[1] != "STEP2-COMPLETE" {
		t.Fatalf("thoughts = %q, want [STEP1-COMPLETE STEP2-COMPLETE]", got)
	}
	// A delta right after must start fresh from STEP2's text, not append.
	next2, _ := next.(Model).Update(delta("more"))
	if got := next2.(Model).reasoning; got != "more" {
		t.Fatalf("delta after two boundaries = %q, want %q", got, "more")
	}
}

// TestReasoningResetPendingClearsAtTurnEnd is the flag-leakage regression: a
// boundary event with no following delta (the turn ends right there) must
// leave neither stale text nor an armed reset flag for the NEXT turn to
// inherit. responseMsg is the normal (non-interrupted) turn-end path.
func TestReasoningResetPendingClearsAtTurnEnd(t *testing.T) {
	m := Model{
		viewport:  viewport.New(80, 20),
		width:     80,
		loading:   true,
		presenter: testPresenter(),
	}

	next, _ := m.Update(boundary("FINAL-STEP"))
	if !next.(Model).reasoningResetPending {
		t.Fatal("setup: boundary should have armed reasoningResetPending")
	}

	next, _ = next.(Model).Update(responseMsg{content: "the answer"})
	got := next.(Model)
	if got.reasoning != "" {
		t.Fatalf("reasoning after turn end = %q, want empty (no stale boundary text)", got.reasoning)
	}
	if got.reasoningResetPending {
		t.Fatal("reasoningResetPending leaked past turn end into the next turn")
	}
}

// TestInterruptedTurnAppendsNoticeAndResetsReasoning: Ctrl+C mid-stream ends
// the turn via the same responseMsg path but with a context.Canceled error.
// It exercises the SAME two reasoning-reset lines as
// TestReasoningResetPendingClearsAtTurnEnd (responseMsg resets
// m.reasoning/reasoningResetPending unconditionally before branching on
// msg.err, so those two lines pass or fail in lockstep regardless of which
// branch runs) — what THIS test actually pins beyond that restatement is the
// interrupted-transcript line: the "interrupted." notice lands in the
// transcript instead of the turn's partial reply being silently dropped.
func TestInterruptedTurnAppendsNoticeAndResetsReasoning(t *testing.T) {
	m := Model{
		viewport:  viewport.New(80, 20),
		width:     80,
		loading:   true,
		presenter: testPresenter(),
	}

	// Mid-stream: a delta, then a boundary (arms the flag), simulating an
	// interrupt landing right after a step completed.
	next, _ := m.Update(delta("partial thought"))
	next, _ = next.(Model).Update(boundary("STEP-COMPLETE"))
	if !next.(Model).reasoningResetPending {
		t.Fatal("setup: boundary should have armed reasoningResetPending")
	}

	next, _ = next.(Model).Update(responseMsg{err: context.Canceled})
	got := next.(Model)
	if got.reasoning != "" {
		t.Fatalf("reasoning after interrupt = %q, want empty", got.reasoning)
	}
	if got.reasoningResetPending {
		t.Fatal("reasoningResetPending leaked past an interrupted turn")
	}
	found := false
	for _, msg := range got.messages {
		if msg.Role == "agent" && msg.Content == "interrupted." {
			found = true
		}
	}
	if !found {
		t.Fatal("interrupted turn did not append the \"interrupted.\" notice")
	}
}

// TestReasoningResetPendingClearsOnPark exercises the OTHER turn-boundary
// reset site (parkMsg, not responseMsg): a boundary with no following delta,
// where the run parks instead of fully ending. Same requirement — no stale
// text, no leaked flag — through a different code path.
func TestReasoningResetPendingClearsOnPark(t *testing.T) {
	m := Model{
		viewport:  viewport.New(80, 20),
		textarea:  textarea.New(),
		width:     80,
		loading:   true,
		presenter: testPresenter(),
	}

	next, _ := m.Update(boundary("FINAL-STEP"))
	if !next.(Model).reasoningResetPending {
		t.Fatal("setup: boundary should have armed reasoningResetPending")
	}

	next, _ = next.(Model).Update(parkMsg{parked: true, reply: "parked reply"})
	got := next.(Model)
	if got.reasoning != "" {
		t.Fatalf("reasoning after park = %q, want empty (no stale boundary text)", got.reasoning)
	}
	if got.reasoningResetPending {
		t.Fatal("reasoningResetPending leaked past a park into whatever runs next")
	}
}

// TestListenReasoningEventsCoalescesABurst confirms the listener drains
// whatever is already queued on the channel into a single message rather than
// returning one message per event — the mechanism that keeps a fast token
// stream from forcing an updateViewport per token — and that it preserves
// arrival order across a mix of delta and boundary kinds.
func TestListenReasoningEventsCoalescesABurst(t *testing.T) {
	m := Model{
		ctx:           context.Background(),
		reasoningChan: make(chan reasoningEvent, 8),
	}
	m.reasoningChan <- reasoningEvent{kind: reasoningEventDelta, text: "a"}
	m.reasoningChan <- reasoningEvent{kind: reasoningEventDelta, text: "b"}
	m.reasoningChan <- reasoningEvent{kind: reasoningEventBoundary, text: "ab-complete"}

	msg := m.listenReasoningEvents()()
	got, ok := msg.(reasoningEventsMsg)
	if !ok {
		t.Fatalf("listenReasoningEvents() returned %T, want reasoningEventsMsg", msg)
	}
	want := reasoningEventsMsg{
		{kind: reasoningEventDelta, text: "a"},
		{kind: reasoningEventDelta, text: "b"},
		{kind: reasoningEventBoundary, text: "ab-complete"},
	}
	if len(got) != len(want) {
		t.Fatalf("coalesced batch has %d events, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d = %+v, want %+v (order not preserved)", i, got[i], want[i])
		}
	}

	// The channel is drained: a second call blocks until fed again, i.e. it
	// does not re-deliver anything left over from the burst.
	select {
	case m.reasoningChan <- reasoningEvent{kind: reasoningEventDelta, text: "d"}:
	default:
		t.Fatal("channel unexpectedly full after drain")
	}
	msg2 := m.listenReasoningEvents()()
	got2 := msg2.(reasoningEventsMsg)
	if len(got2) != 1 || got2[0].text != "d" {
		t.Fatalf("post-drain batch = %+v, want a single %q delta", got2, "d")
	}
}

// TestListenReasoningEventsPreservesOrderUnderRealConcurrency exercises the
// REAL reasoningChan and the REAL listenReasoningEvents goroutine path (not
// Update called synchronously in-process): a producer goroutine sends a mix
// of delta and boundary events with real scheduling in between sends, while
// this test repeatedly invokes the listener's returned tea.Cmd exactly the
// way bubbletea would after each re-arm, and flattens the batches it gets
// back. This is the scenario the single-channel design exists for: it is the
// two-channel version of this same test that could have shown a boundary
// overtaking a still-buffered final delta.
//
// It does not stand up a full tea.Program (bubbletea's own per-Cmd goroutine
// dispatch is not exercised here) — that infrastructure doesn't exist in this
// package's test harness and adding it is out of scope for this fix. What it
// does prove: with everything funneled through one channel, concurrent
// producer/consumer scheduling cannot reorder what Update eventually sees,
// because there is only one FIFO queue in play.
func TestListenReasoningEventsPreservesOrderUnderRealConcurrency(t *testing.T) {
	m := Model{
		ctx:           context.Background(),
		reasoningChan: make(chan reasoningEvent, 4), // small: forces real blocking/handoff
	}

	want := reasoningEventsMsg{
		{kind: reasoningEventDelta, text: "Let"},
		{kind: reasoningEventDelta, text: " me"},
		{kind: reasoningEventDelta, text: " think"},
		{kind: reasoningEventBoundary, text: "Let me think"},
		{kind: reasoningEventDelta, text: "Next"},
		{kind: reasoningEventDelta, text: " step"},
		{kind: reasoningEventBoundary, text: "Next step"},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, ev := range want {
			m.reasoningChan <- ev
		}
	}()

	var got reasoningEventsMsg
	for len(got) < len(want) {
		msg := m.listenReasoningEvents()()
		batch, ok := msg.(reasoningEventsMsg)
		if !ok {
			t.Fatalf("listenReasoningEvents() returned %T, want reasoningEventsMsg", msg)
		}
		got = append(got, batch...)
	}
	<-done

	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d = %+v, want %+v — producer order not preserved under real concurrency", i, got[i], want[i])
		}
	}
}

// assistantMessages returns the Content of every "assistant"-role transcript
// entry, in order — the shape most content-streaming assertions below need.
func assistantMessages(m Model) []string {
	var out []string
	for _, msg := range m.messages {
		if msg.Role == "assistant" {
			out = append(out, msg.Content)
		}
	}
	return out
}

// TestContentDeltaAppendsIntoInProgressAssistantMessage feeds streamed
// "content" deltas (Callbacks.OnStream's answer-text kind) through Update and
// asserts they land as ONE assistant transcript message, concatenated in
// arrival order — the reply growing in place rather than one bubble per
// delta.
func TestContentDeltaAppendsIntoInProgressAssistantMessage(t *testing.T) {
	m := Model{
		viewport:  viewport.New(80, 20),
		width:     80,
		loading:   true,
		presenter: testPresenter(),
	}

	var next tea.Model = m
	for _, chunk := range []string{"The", " quick", " brown", " fox"} {
		next, _ = next.(Model).Update(content(chunk))
	}

	got := assistantMessages(next.(Model))
	want := []string{"The quick brown fox"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("assistant messages = %+v, want %+v (deltas should accumulate into one in-progress message)", got, want)
	}
}

// TestContentDeltaIgnoredWhenNoTurnInFlight guards the failure mode a stray,
// late-arriving content delta would cause: with no turn loading, a delta must
// not fabricate a new assistant bubble out of nowhere.
func TestContentDeltaIgnoredWhenNoTurnInFlight(t *testing.T) {
	m := Model{
		viewport:  viewport.New(80, 20),
		width:     80,
		loading:   false,
		presenter: testPresenter(),
	}

	next, _ := m.Update(content("stray tail fragment"))
	if got := assistantMessages(next.(Model)); len(got) != 0 {
		t.Fatalf("assistant messages = %+v, want none (delta arrived with no turn in flight)", got)
	}
}

// TestResponseMsgReconcilesStreamedReplyWithoutDuplicating is the core
// duplicate-suppression test: content deltas already built the in-progress
// assistant message, so the terminal responseMsg — which carries the SAME
// text as its own payload, exactly like the pre-streaming non-streamed path
// always has — must reconcile that one message rather than appending a
// second copy of the reply.
func TestResponseMsgReconcilesStreamedReplyWithoutDuplicating(t *testing.T) {
	m := Model{
		viewport:  viewport.New(80, 20),
		width:     80,
		loading:   true,
		presenter: testPresenter(),
	}

	next, _ := m.Update(content("Hel"))
	next, _ = next.(Model).Update(content("lo"))
	next, _ = next.(Model).Update(responseMsg{content: "Hello"})

	got := assistantMessages(next.(Model))
	want := []string{"Hello"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("assistant messages = %+v, want %+v (responseMsg must reconcile, not duplicate, the streamed reply)", got, want)
	}
}

// TestContentDeltaStartsFreshMessagePerTurn confirms the turn boundary
// actually closes the in-progress message: a delta streamed for the NEXT
// turn must start a new assistant bubble, not keep appending onto the
// previous turn's already-finalized reply.
func TestContentDeltaStartsFreshMessagePerTurn(t *testing.T) {
	m := Model{
		viewport:  viewport.New(80, 20),
		width:     80,
		loading:   true,
		presenter: testPresenter(),
	}

	next, _ := m.Update(content("first reply"))
	next, _ = next.(Model).Update(responseMsg{content: "first reply"})

	// Next turn starts.
	nm := next.(Model)
	nm.loading = true
	next, _ = nm.Update(content("second reply"))

	got := assistantMessages(next.(Model))
	want := []string{"first reply", "second reply"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("assistant messages = %+v, want %+v (second turn must not append onto the first turn's reply)", got, want)
	}
}

// TestParkMsgReconcilesStreamedReplyWithoutDuplicating mirrors the
// responseMsg reconciliation test for the OTHER turn-boundary path: a run
// that parks mid-stream must fold the already-streamed text into the SAME
// message the park delivers, not add a second copy — and must still arm
// lastParkedReply so a later responseMsg carrying the identical final text
// (a run that parks and then ends with no further generation) does not
// duplicate it either.
func TestParkMsgReconcilesStreamedReplyWithoutDuplicating(t *testing.T) {
	m := Model{
		viewport:  viewport.New(80, 20),
		textarea:  textarea.New(),
		width:     80,
		loading:   true,
		presenter: testPresenter(),
	}

	next, _ := m.Update(content("partial "))
	next, _ = next.(Model).Update(content("reply"))
	next, _ = next.(Model).Update(parkMsg{parked: true, reply: "partial reply"})

	got := assistantMessages(next.(Model))
	want := []string{"partial reply"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("assistant messages = %+v, want %+v (park must reconcile, not duplicate, the streamed reply)", got, want)
	}
	if got := next.(Model).lastParkedReply; got != "partial reply" {
		t.Fatalf("lastParkedReply = %q, want %q", got, "partial reply")
	}

	// The run parked and then ended with the identical reply and no further
	// generation: responseMsg must not duplicate it.
	next, _ = next.(Model).Update(responseMsg{content: "partial reply"})
	got2 := assistantMessages(next.(Model))
	if len(got2) != 1 || got2[0] != "partial reply" {
		t.Fatalf("assistant messages after responseMsg = %+v, want %+v", got2, want)
	}
}

// TestStreamingUnclosedMarkdownRendersOnceClosed: an unclosed bold run is not
// markdown yet, so while it streams its markers stay visible as text. Once
// responseMsg finalizes the reply with the closed run, the markers are gone.
// (Complete blocks render as markdown while streaming — see
// TestStreamingRenderMatchesFinalRender.)
func TestStreamingUnclosedMarkdownRendersOnceClosed(t *testing.T) {
	m := Model{
		viewport:  viewport.New(80, 20),
		width:     80,
		loading:   true,
		presenter: testPresenter(),
	}

	next, _ := m.Update(content("**bold"))
	mid := drainStreamReveal(t, next.(Model))
	if out := mid.viewport.View(); !strings.Contains(out, "**bold") {
		t.Fatalf("mid-stream viewport does not show raw markdown markers: %q", out)
	}

	next, _ = mid.Update(responseMsg{content: "**bold**"})
	done := next.(Model)
	if out := done.viewport.View(); strings.Contains(out, "**bold**") {
		t.Fatalf("finalized viewport still shows raw markdown markers (glamour not applied): %q", out)
	}
}

// TestStaleContentDeltaAcrossToolCallDoesNotFabricateBubble reproduces the
// orphan-bubble bug found in review: m.loading alone does not guard the
// window it needs to. m.loading is a single session-wide bool that turn N+1
// flips back to true as soon as it dispatches, independent of whether turn
// N's own trailing deltas have finished draining — so a stale turn-N delta
// sails through the loading guard once turn N+1 is under way.
//
// Because responseMsg already cleared streamingActive when turn N ended,
// that stale delta fabricates a brand-new bubble (appendStreamedContent sees
// no live streaming target and starts one). If turn N+1 then streams
// straight into it, responseMsg's reconciliation overwrites it wholesale —
// harmless. But if a TOOL CALL lands first (a real "tool" transcript entry,
// appended via appendMessage — which clears streamingActive as its own side
// effect), the bogus bubble is orphaned: it is no longer the tail once
// responseMsg reconciles turn N+1's own (separate) streaming message, so it
// is never touched, never cleaned up, and persists in the transcript
// (autosaved) as a permanent, misattributed fabricated entry.
//
// The fix: reasoningEvent carries the turn generation it was stamped with
// at OnStream enqueue time (turnGen, bumped once per real turn dispatch in
// sendMessage/sendWithAttachmentsCmd); Update drops a delta whose gen
// doesn't match the CURRENT generation before it ever reaches
// appendStreamedContent, so a stale turn-N delta arriving during turn N+1
// never creates anything at all.
func TestStaleContentDeltaAcrossToolCallDoesNotFabricateBubble(t *testing.T) {
	m := Model{
		viewport:  viewport.New(80, 20),
		width:     80,
		loading:   true,
		presenter: testPresenter(),
		turnGen:   new(atomic.Int32), // turn N's dispatch left this at generation 0
	}

	// Turn N streams and ends normally (gen 0).
	next, _ := m.Update(reasoningEventsMsg{{kind: reasoningEventContentDelta, text: "turn one reply", gen: 0}})
	next, _ = next.(Model).Update(responseMsg{content: "turn one reply"})
	cur := next.(Model)
	if cur.loading {
		t.Fatal("setup: turn one should have ended")
	}

	// Turn N+1 dispatches for real, through the actual production bump path
	// (sendMessage), the same way dispatchResolved's KindSend branch would —
	// without invoking the returned Cmd itself, which needs a live session;
	// the bump happens synchronously before that Cmd ever runs.
	cur.loading = true
	_ = cur.sendMessage("question two")

	// A STALE delta from turn N (still tagged gen 0) arrives late — after
	// turn N ended AND after turn N+1 has already started loading. This is
	// exactly the window m.loading alone cannot close.
	next, _ = cur.Update(reasoningEventsMsg{{kind: reasoningEventContentDelta, text: "STALE TAIL", gen: 0}})
	cur = next.(Model)

	// A tool call/result lands mid turn N+1, as it would for a real
	// multi-step turn — any transcript append here orphans a fabricated
	// bubble under the old code.
	cur.appendMessage(ChatMessage{Role: "tool", Content: "tool ran"})

	// Turn N+1's own real reply streams in (gen 1) and finishes.
	next, _ = cur.Update(reasoningEventsMsg{{kind: reasoningEventContentDelta, text: "turn two reply", gen: 1}})
	next, _ = next.(Model).Update(responseMsg{content: "turn two reply"})
	cur = next.(Model)

	got := assistantMessages(cur)
	want := []string{"turn one reply", "turn two reply"}
	if len(got) != len(want) {
		t.Fatalf("assistant messages = %+v, want %+v (a stale turn-N delta fabricated an orphaned bubble)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("assistant messages = %+v, want %+v", got, want)
		}
	}
}

// TestStaleBoundaryDoesNotRepaintPreviousThinking is the thinking-box flicker:
// the previous step's trace reappearing for a single frame just after the user
// sends the next message.
//
// reasoningEventBoundary was the one kind Update did NOT check against
// turnGen. The tolerance was argued from turn end: a stale boundary only ever
// overwrites m.reasoning with a complete (if outdated) block, and responseMsg
// clears that at every turn end anyway. But the boundary does not have to
// arrive BEFORE that reset — reasoningChan is buffered and bubbletea relays
// each listen Cmd on its own goroutine, so turn N's last boundary can land
// after responseMsg already cleared the box and after turn N+1 has dispatched.
// It then repaints turn N's trace into an empty box, where it stays until
// turn N+1's first delta disarms reasoningResetPending and starts over: one
// frame of the previous answer's thinking, every time the user writes.
func TestStaleBoundaryDoesNotRepaintPreviousThinking(t *testing.T) {
	m := Model{
		viewport:  viewport.New(80, 20),
		width:     80,
		loading:   true,
		presenter: testPresenter(),
		turnGen:   new(atomic.Int32), // turn N runs at generation 0
	}

	// Turn N thinks, then ends — responseMsg clears the box.
	next, _ := m.Update(reasoningEventsMsg{{kind: reasoningEventDelta, text: "turn one thinking", gen: 0}})
	next, _ = next.(Model).Update(responseMsg{content: "turn one reply"})
	cur := next.(Model)
	if cur.reasoning != "" {
		t.Fatalf("setup: reasoning after turn end = %q, want empty", cur.reasoning)
	}

	// The user writes: turn N+1 dispatches for real, bumping the generation.
	cur.loading = true
	_ = cur.sendMessage("question two")

	// Turn N's step boundary arrives late, still tagged gen 0.
	next, _ = cur.Update(reasoningEventsMsg{{kind: reasoningEventBoundary, text: "turn one thinking", gen: 0}})
	cur = next.(Model)
	if cur.reasoning != "" {
		t.Fatalf("reasoning = %q after a stale boundary, want empty (previous thinking repainted)", cur.reasoning)
	}
	if cur.reasoningResetPending {
		t.Fatal("a stale boundary armed reasoningResetPending for a turn it does not belong to")
	}

	// Turn N+1's own trace still streams normally.
	next, _ = cur.Update(reasoningEventsMsg{{kind: reasoningEventDelta, text: "turn two thinking", gen: cur.currentTurnGen()}})
	if got := next.(Model).reasoning; got != "turn two thinking" {
		t.Fatalf("reasoning = %q, want %q", got, "turn two thinking")
	}
}

// TestRefreshContextTokensOnlyRunsDuringATurn pins the two guards on the
// mid-turn gauge refresh. The figure itself is pinned on the session side
// (chat.TestContextTokensTracksTheRunInFlight): a zero-value chat.Session
// reports 0 tokens, which is exactly the "nothing to say yet" case the
// refresh must not write through.
//
// Between turns the boundary handlers (responseMsg, parkMsg, the compaction
// notices) own m.contextTokens — a compaction notice in particular writes the
// POST-compaction size, and a tick that re-read the session behind it would
// put the pre-compaction number back on screen.
func TestRefreshContextTokensOnlyRunsDuringATurn(t *testing.T) {
	m := &Model{contextTokens: 4242}

	m.session = nil
	m.loading = true
	m.refreshContextTokens()
	if m.contextTokens != 4242 {
		t.Fatalf("contextTokens = %d with no session, want it untouched", m.contextTokens)
	}

	m.session = &chat.Session{}
	m.loading = false
	m.refreshContextTokens()
	if m.contextTokens != 4242 {
		t.Fatalf("contextTokens = %d between turns, want it untouched", m.contextTokens)
	}

	// Loading, but the session has nothing to report: a zero must not blank
	// the gauge that the last turn left standing.
	m.loading = true
	m.refreshContextTokens()
	if m.contextTokens != 4242 {
		t.Fatalf("contextTokens = %d after a zero reading, want it untouched", m.contextTokens)
	}
}

// TestBoundaryAfterTurnEndDoesNotReappearNextTurn is the window the check on
// dispatch alone leaves open: turn N's step boundary is sent before the run
// returns, but reaches Update AFTER responseMsg cleared the box and BEFORE
// turn N+1 dispatches. The generation has not moved yet, so the boundary
// looked current and wrote turn N's thinking into the hidden box, where the
// next turn showed it until its own first token. responseMsg and parkMsg now
// move the generation too, so an event stamped by the ended turn is dropped.
func TestBoundaryAfterTurnEndDoesNotReappearNextTurn(t *testing.T) {
	for name, end := range map[string]tea.Msg{
		"response": responseMsg{content: "turn one reply"},
		"park":     parkMsg{parked: true, reply: "turn one reply"},
	} {
		t.Run(name, func(t *testing.T) {
			m := Model{
				viewport:  viewport.New(80, 20),
				textarea:  textarea.New(),
				width:     80,
				loading:   true,
				presenter: testPresenter(),
				turnGen:   new(atomic.Int32),
			}
			next, _ := m.Update(end)
			next, _ = next.(Model).Update(reasoningEventsMsg{{kind: reasoningEventBoundary, text: "turn one thinking", gen: 0}})
			cur := next.(Model)
			if cur.reasoning != "" {
				t.Fatalf("reasoning = %q after the turn ended, want empty (shown again next turn)", cur.reasoning)
			}
		})
	}
}

// Tool wakeups and reasoning listener commands can reach Update in either
// order. Only the reasoning stream may close a thought step.
func TestToolMailboxReasoningOrder(t *testing.T) {
	for _, mailboxFirst := range []bool{true, false} {
		name := "delayed tool mailbox"
		if mailboxFirst {
			name = "delayed authoritative boundary"
		}
		t.Run(name, func(t *testing.T) {
			m := runningTestModel()
			m.reasoningChan = make(chan reasoningEvent, 16)
			m.toolEvents = newToolEventQueue()
			m.toolEvents.begin()
			start, _ := m.toolCallbacks()
			m = update(m, delta("partial thought"))
			m = update(m, content("checking now"))
			// The content folded this thought, but the authoritative text
			// remains queued when the tool starts.
			m.reasoningChan <- reasoningEvent{kind: reasoningEventBoundary, text: "complete thought"}
			start(chat.ToolStart{ID: "read-1", Name: "read"})
			if mailboxFirst {
				m = update(m, toolEventsReadyMsg{})
			}
			m = update(m, m.listenReasoningEvents()())
			got := thoughts(m)
			if len(got) != 1 || got[0] != "complete thought" {
				t.Fatalf("thoughts = %q, want one authoritative thought", got)
			}
			if !m.reasoningResetPending {
				t.Fatal("step end did not arm reasoning reset")
			}
			m = update(m, delta("next thought"))
			if !mailboxFirst {
				m = update(m, toolEventsReadyMsg{})
			}
			if m.reasoning != "next thought" || len(thoughts(m)) != 1 {
				t.Fatalf("tool mailbox folded next step: live=%q thoughts=%q", m.reasoning, thoughts(m))
			}
			if len(m.running) != 1 {
				t.Fatalf("running tools = %d, want 1", len(m.running))
			}
		})
	}
}

func TestToolStartEmitsGenerationSafeReasoningMarker(t *testing.T) {
	m := runningTestModel()
	m.turnGen = new(atomic.Int32)
	m.reasoningChan = make(chan reasoningEvent, 16)
	m.toolEvents = newToolEventQueue()
	m.toolEvents.begin()
	start, _ := m.toolCallbacks()
	start(chat.ToolStart{ID: "old", Name: "read"})
	if len(m.reasoningChan) != 1 {
		t.Fatal("tool start must enqueue a step-end marker on the reasoning stream")
	}
	marker := <-m.reasoningChan
	if marker.kind != reasoningEventStepEnd || marker.gen != m.currentTurnGen() {
		t.Fatalf("unexpected step-end marker: %+v", marker)
	}
	m = update(m, delta("tool-only thought"))
	m = update(m, reasoningEventsMsg{marker})
	if m.reasoning != "" || !m.reasoningResetPending || len(thoughts(m)) != 1 {
		t.Fatal("tool-only step did not fold and arm reset")
	}
	// Parking advances the reasoning generation, but resumes the same
	// SendMessage and therefore reuses its captured tool callback.
	m = update(m, parkMsg{parked: true})
	m = update(m, parkMsg{parked: false})
	resumedGen := m.currentTurnGen()
	m = update(m, reasoningEventsMsg{{kind: reasoningEventDelta, text: "resumed thought", gen: resumedGen}})
	start(chat.ToolStart{ID: "resumed", Name: "read"})
	resumedMarker := <-m.reasoningChan
	if resumedMarker.gen != resumedGen || resumedMarker.toolGen != marker.toolGen {
		t.Fatalf("resumed marker must keep its run and use the current epoch: %+v", resumedMarker)
	}
	m.reasoningChan <- resumedMarker
	m = update(m, toolEventsReadyMsg{})
	if m.reasoning != "resumed thought" {
		t.Fatal("tool mailbox must not fold the resumed thought")
	}
	m = update(m, m.listenReasoningEvents()())
	if m.reasoning != "" || !m.reasoningResetPending {
		t.Fatalf("resumed tool start did not close thought: live=%q reset=%v", m.reasoning, m.reasoningResetPending)
	}
	got := thoughts(m)
	if len(got) != 2 || got[1] != "resumed thought" {
		t.Fatalf("thoughts = %q, want ordered resumed thought", got)
	}
	m.toolEvents.end()
	start(chat.ToolStart{ID: "ended", Name: "read"})
	if len(m.reasoningChan) != 0 {
		t.Fatal("ended run callback enqueued a reasoning marker")
	}
	m.sendMessage("new run") // dispatch only; no session needed
	m = update(m, reasoningEventsMsg{{kind: reasoningEventDelta, text: "new turn", gen: m.currentTurnGen()}})
	// An already queued marker and a callback retained by an old producer
	// must not close the new turn's live trace.
	m = update(m, reasoningEventsMsg{marker})
	// A run can end between the callback's lifecycle check and epoch read.
	// Even a marker stamped with the new epoch must retain its old run ID.
	marker.gen = m.currentTurnGen()
	m = update(m, reasoningEventsMsg{marker})
	start(chat.ToolStart{ID: "late-old", Name: "read"})
	if len(m.reasoningChan) != 0 {
		t.Fatal("old run callback enqueued a reasoning marker")
	}
	for len(m.reasoningChan) > 0 {
		m = update(m, reasoningEventsMsg{<-m.reasoningChan})
	}
	if m.reasoning != "new turn" || m.reasoningResetPending {
		t.Fatalf("stale tool start closed new thought: live=%q reset=%v", m.reasoning, m.reasoningResetPending)
	}
}
