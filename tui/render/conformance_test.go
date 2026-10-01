package render_test

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
	"github.com/mudler/nib/tui/render/full"
	"github.com/mudler/nib/tui/render/inline"
)

// presenters is every implementation. Adding one here is how a new surface
// proves it behaves like the others.
func presenters() map[string]render.Presenter {
	return map[string]render.Presenter{
		"inline": inline.New(),
		"full":   full.New(),
	}
}

// presenterNames returns the presenter keys in a stable order, so the first is
// always the same reference every other implementation is compared against and
// a failure names a deterministic pair.
func presenterNames() []string {
	var names []string
	for name := range presenters() {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;]*m")

// stripANSI removes SGR escape sequences so width/content checks measure only
// visible runes.
func stripANSI(s string) string {
	return ansiEscape.ReplaceAllString(s, "")
}

// The conformance suite below enforces the phase's one structural rule: the two
// surfaces diverge in CHROME but never in LOGIC. `full` is currently a copy of
// `inline` and Phase 3 deliberately pulls their chrome apart (a gutter instead
// of labels, framed dialogs), so a shared base implementation would have to be
// re-cut immediately. These tests are the guard that replaces it: they compare
// the two presenters' STRUCTURE — how many blocks, in what order, with the
// input's content in the same relative positions, the same trailing-separator
// decision, the same option count — while staying blind to the styling,
// glyphs, prefixes and extra frame rows that are each surface's own business.
//
// A structural fingerprint is deliberately made of things chrome cannot
// change:
//
//   - blocks: how many blank-line-separated groups the output has. Restyling a
//     line or prefixing it with a gutter does not regroup the output; dropping
//     or merging a block does.
//   - trailingSep: whether the chunk ends in the blank-line separator before
//     the next one. This is the HugNext rule, which is logic, not decoration.
//   - sequence: the order the input's own substrings come back in, and which of
//     them share a line ("|" same line, "/" a later line). Immune to prefixes
//     and to extra frame rows, sensitive to content being dropped, reordered,
//     or collapsed onto one line.

// fingerprint is the chrome-blind structural signature of one rendered chunk.
type fingerprint struct {
	Blocks      int
	TrailingSep bool
	Sequence    string
}

// blocks counts the blank-line-separated groups in a rendered chunk. A "blank"
// line is one with no visible content at all, so a gutter-only spacer row (the
// approval menu's leading `▏` line) correctly does NOT split a block — it is
// chrome inside one.
func blocks(out string) int {
	n := 0
	inBlock := false
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(stripANSI(line)) == "" {
			inBlock = false
			continue
		}
		if !inBlock {
			n++
			inBlock = true
		}
	}
	return n
}

// sequence reports the order tokens appear in the output and which of them
// share a line: tokens on one line are joined with "|", a move to a later line
// is "/". Tokens the output dropped are omitted here and reported separately by
// assertPreserves, so a drop shows up as both a missing substring and a
// structural difference.
func sequence(out string, tokens []string) string {
	type hit struct {
		line, col int
		token     string
	}
	var hits []hit
	lines := strings.Split(stripANSI(out), "\n")
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		for i, line := range lines {
			if col := strings.Index(line, tok); col >= 0 {
				hits = append(hits, hit{line: i, col: col, token: tok})
				break
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].line != hits[j].line {
			return hits[i].line < hits[j].line
		}
		return hits[i].col < hits[j].col
	})
	var b strings.Builder
	for i, h := range hits {
		if i > 0 {
			if h.line == hits[i-1].line {
				b.WriteString("|")
			} else {
				b.WriteString("/")
			}
		}
		b.WriteString(h.token)
	}
	return b.String()
}

func fingerprintOf(out string, tokens []string) fingerprint {
	return fingerprint{
		Blocks:      blocks(out),
		TrailingSep: strings.HasSuffix(out, "\n\n"),
		Sequence:    sequence(out, tokens),
	}
}

// assertPreserves fails when the rendered output dropped any of the input's own
// text. Chrome is a presenter's business; the user's words are not.
func assertPreserves(t *testing.T, name, out string, tokens []string) {
	t.Helper()
	plain := stripANSI(out)
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		if !strings.Contains(plain, tok) {
			t.Errorf("%s dropped %q from its output: %q", name, tok, out)
		}
	}
}

// assertFitsWidth fails when any line exceeds the budget, which would corrupt
// the surrounding shell on the inline widget and wrap unpredictably on the
// alt screen.
func assertFitsWidth(t *testing.T, name, out string, w int) {
	t.Helper()
	for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if got := lipgloss.Width(stripANSI(line)); got > w {
			t.Errorf("%s line %d is %d cells wide, budget %d: %q", name, i, got, w, line)
		}
	}
}

// assertSameFingerprint compares every presenter against the first one by
// name. Differing fingerprints mean the surfaces disagree about something
// other than chrome.
func assertSameFingerprint(t *testing.T, fps map[string]fingerprint) {
	t.Helper()
	names := presenterNames()
	ref := names[0]
	for _, name := range names[1:] {
		if fps[name] != fps[ref] {
			t.Errorf("structural drift between %s and %s:\n %s = %+v\n %s = %+v",
				ref, name, ref, fps[ref], name, fps[name])
		}
	}
}

// TestAllPresentersRenderMessageContent: chrome may differ, the user's words may not.
func TestAllPresentersRenderMessageContent(t *testing.T) {
	for name, p := range presenters() {
		t.Run(name, func(t *testing.T) {
			out := p.Message(render.Message{Role: render.RoleUser, Content: "distinctive content"}, render.RoleNone, 80)
			if !strings.Contains(out, "distinctive content") {
				t.Errorf("%s dropped the message content: %q", name, out)
			}
		})
	}
}

// TestAllPresentersRespectWidth: no presenter may emit a line wider than the
// budget it was given, or the inline widget corrupts the surrounding shell.
func TestAllPresentersRespectWidth(t *testing.T) {
	const w = 40
	for name, p := range presenters() {
		t.Run(name, func(t *testing.T) {
			// RoleUser deliberately: Message wraps user content itself. Assistant
			// and agent content arrives pre-rendered (glamour is width-cached
			// state owned by the model), so it is not the presenter's to wrap.
			out := p.Message(render.Message{
				Role:    render.RoleUser,
				Content: strings.Repeat("overflowing ", 30),
			}, render.RoleNone, w)
			for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
				if got := lipgloss.Width(stripANSI(line)); got > w {
					t.Errorf("%s line %d is %d cells wide, budget %d: %q", name, i, got, w, line)
				}
			}
		})
	}
}

// TestMessageStructuralEquivalence walks every Role (and the flags that change
// how one is laid out) and requires all presenters to agree structurally:
// same blocks, same content order, same trailing-separator decision. wantSep is
// stated per case rather than merely compared, so the two surfaces drifting
// TOGETHER away from the HugNext rule is a failure too.
func TestMessageStructuralEquivalence(t *testing.T) {
	const w = 60
	cases := []struct {
		name    string
		msg     render.Message
		prev    render.Role
		tokens  []string
		wantSep bool
		// wraps marks the roles whose content the presenter itself wraps;
		// pre-rendered (glamour) content is not the presenter's to fit.
		wraps bool
	}{
		{
			name:    "user",
			msg:     render.Message{Role: render.RoleUser, Content: "user says this"},
			tokens:  []string{"user says this"},
			wantSep: true,
			wraps:   true,
		},
		{
			name:    "user after assistant",
			msg:     render.Message{Role: render.RoleUser, Content: "second turn"},
			prev:    render.RoleAssistant,
			tokens:  []string{"second turn"},
			wantSep: true,
			wraps:   true,
		},
		{
			// Phase 3 Task 12: inline drops the "you ·" label on a consecutive
			// same-role message (relying on the blank-line separator instead) and
			// full always shows its gutter regardless of prev — a deliberate
			// CHROME divergence. This case proves it stops there: content,
			// blocks, and the trailing separator must still agree across
			// surfaces even though the two would render visibly different bytes.
			name:    "consecutive user messages",
			msg:     render.Message{Role: render.RoleUser, Content: "third turn"},
			prev:    render.RoleUser,
			tokens:  []string{"third turn"},
			wantSep: true,
			wraps:   true,
		},
		{
			name:    "assistant",
			msg:     render.Message{Role: render.RoleAssistant, Content: "assistant answer"},
			tokens:  []string{"assistant answer"},
			wantSep: true,
		},
		{
			// Same rule as "consecutive user messages" above, for the other role
			// Task 12 touches.
			name:    "consecutive assistant messages",
			msg:     render.Message{Role: render.RoleAssistant, Content: "second answer"},
			prev:    render.RoleAssistant,
			tokens:  []string{"second answer"},
			wantSep: true,
		},
		{
			name:    "agent",
			msg:     render.Message{Role: render.RoleAgent, Content: "agent started", AgentID: "abcdef123456"},
			tokens:  []string{"agent started"},
			wantSep: true,
		},
		{
			// The HugNext rule: the thread run that follows hugs this line, so
			// the separator must be omitted. This is logic, not decoration —
			// both surfaces owe the same answer.
			name:    "agent hugging its thread run",
			msg:     render.Message{Role: render.RoleAgent, Content: "agent started", AgentID: "abcdef123456", HugNext: true},
			tokens:  []string{"agent started"},
			wantSep: false,
		},
		{
			name:    "tool",
			msg:     render.Message{Role: render.RoleTool, Label: "read file.go", Content: "tool output body"},
			tokens:  []string{"read file.go", "tool output body"},
			wantSep: true,
			wraps:   true,
		},
		{
			name:    "tool from a sub-agent",
			msg:     render.Message{Role: render.RoleTool, Label: "read file.go", Content: "tool output body", AgentID: "abcdef123456"},
			tokens:  []string{"read file.go", "tool output body"},
			wantSep: true,
			wraps:   true,
		},
		{
			name:    "error",
			msg:     render.Message{Role: render.RoleError, Content: "it went wrong"},
			tokens:  []string{"it went wrong"},
			wantSep: true,
			wraps:   true,
		},
		{
			name:   "multiline content",
			msg:    render.Message{Role: render.RoleAssistant, Content: "first para\n\nsecond para"},
			tokens: []string{"first para", "second para"},
			// Two paragraphs must stay two blocks and stay in order; the
			// fingerprint comparison is what enforces that across surfaces.
			wantSep: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fps := map[string]fingerprint{}
			for name, p := range presenters() {
				out := p.Message(tc.msg, tc.prev, w)
				assertPreserves(t, name, out, tc.tokens)
				if tc.wraps {
					assertFitsWidth(t, name, out, w)
				}
				if got := strings.HasSuffix(out, "\n\n"); got != tc.wantSep {
					t.Errorf("%s trailing separator = %v, want %v: %q", name, got, tc.wantSep, out)
				}
				fps[name] = fingerprintOf(out, tc.tokens)
			}
			assertSameFingerprint(t, fps)
		})
	}
}

// TestUnknownRoleRendersNothing pins the shared contract for a Role no
// presenter knows: render nothing at all rather than an unlabelled body.
func TestUnknownRoleRendersNothing(t *testing.T) {
	for name, p := range presenters() {
		if out := p.Message(render.Message{Role: render.RoleNone, Content: "orphan"}, render.RoleNone, 60); out != "" {
			t.Errorf("%s rendered %q for an unknown role, want empty", name, out)
		}
	}
}

// TestDialogStructuralEquivalence covers every DialogKind and every branch
// Dialog has: the pre-rendered ask block, the approval card with structured
// rows, the RowsUnstructured prose fallback, and the option-menu shapes the
// type supports (0, 1 — the free-form edit-mode hint — and multi-option real
// menus of two different sizes, which must both get the leading blank-gutter
// separator since that decision is keyed on "is this a genuine multi-option
// menu", not on any particular count).
func TestDialogStructuralEquivalence(t *testing.T) {
	const w = 60
	approvalRows := [][2]string{{"path", "main.go"}, {"mode", "0644"}}

	cases := []struct {
		name   string
		dialog render.Dialog
		tokens []string
		// options is what every presenter must render one line for.
		options []string
		// wantLeadIn is checked only when options is non-empty: whether the
		// menu must be preceded by a chrome-only lead-in line (the blank
		// gutter row) separating it from the card/hint above.
		wantLeadIn bool
	}{
		{
			name: "ask block",
			dialog: render.Dialog{
				Kind:  render.DialogAsk,
				Title: "which one?\n  1. alpha\n  2. beta",
			},
			tokens: []string{"which one?", "1. alpha", "2. beta"},
		},
		{
			// The real production approval menu (tui/model.go's
			// currentDialogs): five options, none of them a special-cased
			// count in the renderer any more. This is the case that used to
			// be named "approval with four options" and silently stopped
			// matching production once a fifth option (the /yolo "[4] yes to
			// everything this session" choice) was added — a stale fixture
			// whose comment claimed to represent "the classic menu" while
			// exercising a dead branch. Pinned here as the real shape instead.
			name: "approval with the real five-option menu",
			dialog: render.Dialog{
				Kind:  render.DialogApproval,
				Title: "bash wants to run",
				Rows:  approvalRows,
				Hint:  "needs the fix applied",
				Options: []render.DialogOption{
					{Text: theme.ApproveOnce, Emphasis: true},
					{Text: theme.ApproveAlwaysPrefix + "`touch …`" + theme.ApproveAlwaysSuffix, Emphasis: true},
					{Text: theme.ApproveTurn, Emphasis: true},
					{Text: theme.ApproveSession, Emphasis: true},
					{Text: theme.ApproveDenyEdit, Emphasis: false},
				},
			},
			tokens: []string{
				"bash wants to run", "path", "main.go", "mode", "0644", "needs the fix applied",
				theme.ApproveOnce, theme.ApproveAlwaysPrefix, theme.ApproveTurn, theme.ApproveSession, theme.ApproveDenyEdit,
			},
			options: []string{
				theme.ApproveOnce,
				theme.ApproveAlwaysPrefix + "`touch …`" + theme.ApproveAlwaysSuffix,
				theme.ApproveTurn,
				theme.ApproveSession,
				theme.ApproveDenyEdit,
			},
			wantLeadIn: true,
		},
		{
			name: "approval in edit mode",
			dialog: render.Dialog{
				Kind:    render.DialogApproval,
				Title:   "write main.go",
				Rows:    approvalRows,
				Options: []render.DialogOption{{Text: "enter to send", Emphasis: true}},
			},
			tokens:     []string{"write main.go", "path", "mode", "enter to send"},
			options:    []string{"enter to send"},
			wantLeadIn: false,
		},
		{
			name: "approval with unstructured arguments",
			dialog: render.Dialog{
				Kind:             render.DialogApproval,
				Title:            "run something",
				Rows:             [][2]string{{"", "a raw prose block describing the call"}},
				RowsUnstructured: true,
				Options:          []render.DialogOption{{Text: "[y] yes", Emphasis: true}},
			},
			tokens:     []string{"run something", "a raw prose block describing the call", "[y] yes"},
			options:    []string{"[y] yes"},
			wantLeadIn: false,
		},
		{
			name: "approval with no options",
			dialog: render.Dialog{
				Kind:  render.DialogApproval,
				Title: "write main.go",
				Rows:  approvalRows,
			},
			tokens: []string{"write main.go", "path", "mode"},
		},
		{
			// A second, deliberately different multi-option count than the
			// five-option case above: the lead-in rule must not be a
			// disguised "== 5" check in a different shape.
			name: "approval with a two-option menu",
			dialog: render.Dialog{
				Kind:    render.DialogApproval,
				Title:   "write main.go",
				Options: []render.DialogOption{{Text: "[y] yes", Emphasis: true}, {Text: "[n] no", Emphasis: false}},
			},
			tokens:     []string{"write main.go", "[y] yes", "[n] no"},
			options:    []string{"[y] yes", "[n] no"},
			wantLeadIn: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fps := map[string]fingerprint{}
			for name, p := range presenters() {
				out := p.Dialog(tc.dialog, w)
				assertPreserves(t, name, out, tc.tokens)
				// Every option gets its own line: collapsing two onto one, or
				// silently dropping one, changes what the user can press.
				if got := optionLines(out, tc.options); got != len(tc.options) {
					t.Errorf("%s rendered %d option lines, want %d: %q", name, got, len(tc.options), out)
				}
				// The blank-gutter lead-in is structural (it changes how many
				// visually distinct rows the menu has), not chrome, so it is
				// pinned directly rather than folded into the fingerprint —
				// a fingerprint mismatch would only say "something differs"
				// without naming what.
				if len(tc.options) > 0 {
					if got := hasMenuLeadIn(out, tc.options, tc.tokens); got != tc.wantLeadIn {
						t.Errorf("%s: menu lead-in = %v, want %v: %q", name, got, tc.wantLeadIn, out)
					}
				}
				fps[name] = fingerprintOf(out, tc.tokens)
			}
			assertSameFingerprint(t, fps)
		})
	}
}

// optionLines counts the distinct output lines carrying one of the option
// texts, so two options sharing a line counts as one.
func optionLines(out string, options []string) int {
	if len(options) == 0 {
		return 0
	}
	seen := map[int]bool{}
	lines := strings.Split(stripANSI(out), "\n")
	for i, line := range lines {
		for _, opt := range options {
			if strings.Contains(line, opt) {
				seen[i] = true
			}
		}
	}
	return len(seen)
}

// hasMenuLeadIn reports whether the line immediately before the first option
// line is a chrome-only lead-in: it renders as non-blank (a gutter marker or
// similar), yet carries none of the dialog's own content (title, rows, hint,
// or any option text) — i.e. it is a dedicated spacer row, not a content line
// that merely happens to precede the menu. tokens must be the case's full
// token set (not just its options), or a content line right above the menu
// (e.g. the last argument row in edit mode) would be mistaken for a lead-in
// simply because it doesn't happen to repeat an option's own text. This is
// deliberately blind to what the chrome actually looks like (no gutter glyph
// is hardcoded here), so it holds across presenters that render that spacer
// differently.
func hasMenuLeadIn(out string, options, tokens []string) bool {
	lines := strings.Split(stripANSI(out), "\n")
	first := -1
	for i, line := range lines {
		for _, opt := range options {
			if opt != "" && strings.Contains(line, opt) {
				first = i
				break
			}
		}
		if first >= 0 {
			break
		}
	}
	if first <= 0 {
		return false
	}
	lead := lines[first-1]
	if strings.TrimSpace(lead) == "" {
		return false
	}
	for _, tok := range tokens {
		if tok != "" && strings.Contains(lead, tok) {
			return false
		}
	}
	return true
}

// TestUnknownDialogKindRendersNothing: a kind no presenter handles must produce
// nothing on every surface, not a half-drawn card on one of them. DialogResume
// is deliberately NOT used here any more (Phase 3 Task 15 gave it a real
// rendering, shared with DialogAsk — see TestResumeDialogRendersLikeAsk
// below); an out-of-range Kind stands in as the genuinely-unhandled case.
func TestUnknownDialogKindRendersNothing(t *testing.T) {
	unhandled := render.DialogKind(99)
	for name, p := range presenters() {
		if out := p.Dialog(render.Dialog{Kind: unhandled, Title: "resume?"}, 60); out != "" {
			t.Errorf("%s rendered %q for an unhandled DialogKind, want empty", name, out)
		}
	}
}

// TestResumeDialogRendersLikeAsk pins Task 15's reuse decision: DialogResume
// is rendered by the exact same branch as DialogAsk (see buildResumeDialog,
// tui/resume.go), so a /resume picker with options renders structurally
// identically to an equivalent ask_user dialog — same blocks, same option
// count, same trailing separator — on both presenters.
func TestResumeDialogRendersLikeAsk(t *testing.T) {
	const w = 60
	options := []render.DialogOption{{Text: "fix the bug · 3m ago · 4 messages"}, {Text: "add tests · 1h ago · 2 messages"}}
	tokens := []string{"resume a session", options[0].Text, options[1].Text}

	fps := map[string]fingerprint{}
	for name, p := range presenters() {
		out := p.Dialog(render.Dialog{Kind: render.DialogResume, Title: "resume a session", Options: options, Selected: 1, Hint: "up/down move · enter resume · esc cancel"}, w)
		assertPreserves(t, name, out, tokens)
		if got := optionLines(out, []string{options[0].Text, options[1].Text}); got != 2 {
			t.Errorf("%s rendered %d option lines, want 2: %q", name, got, out)
		}
		fps[name] = fingerprintOf(out, tokens)
	}
	assertSameFingerprint(t, fps)
}

// TestHeaderStructuralEquivalence: the header carries the brand, the cwd and
// (when the approval gate is off) the yolo badge, fits the width it is given,
// and is structurally the same on both surfaces.
func TestHeaderStructuralEquivalence(t *testing.T) {
	for _, autoApprove := range []bool{false, true} {
		v := render.ViewState{Width: 50, Brand: "nib", Cwd: "~/src/project", AutoApprove: autoApprove}
		tokens := []string{"nib", "~/src/project"}
		fps := map[string]fingerprint{}
		for name, p := range presenters() {
			out := p.Header(v)
			assertPreserves(t, name, out, tokens)
			assertFitsWidth(t, name, out, v.Width)
			fps[name] = fingerprintOf(out, tokens)
		}
		assertSameFingerprint(t, fps)
	}
}

// TestReasoningStructuralEquivalence: nothing at all when not loading, and the
// spinner, status verb and trace text in the same order on both surfaces.
func TestReasoningStructuralEquivalence(t *testing.T) {
	const w = 50
	idle := render.ViewState{Loading: false, Spinner: "|", Status: "Working", Reasoning: render.Reasoning{Text: "thinking about it"}}
	for name, p := range presenters() {
		if out := p.Reasoning(idle, w); out != "" {
			t.Errorf("%s rendered %q while not loading, want empty", name, out)
		}
	}

	// collapsedTrace is 8 lines; MaxLines below caps it to the trailing 3
	// (lines 6-8), hiding 5 (lines 1-5). wantHint mirrors exactly what both
	// Reasoning implementations compose from theme.ReasoningMore/Expand, so a
	// hint-composition regression in either surface fails this, not just a
	// generic "some text changed" fingerprint drift.
	collapsedTrace := "line-1\nline-2\nline-3\nline-4\nline-5\nline-6\nline-7\nline-8"
	wantHint := "… 5" + theme.ReasoningMore + theme.ReasoningExpand

	cases := []struct {
		name   string
		state  render.ViewState
		tokens []string
		// absent lists substrings that must NOT survive into the output — the
		// collapsed box's whole point is that the head is dropped, not merely
		// that the tail is present (a bug that showed everything would also
		// pass a tokens-only check).
		absent []string
	}{
		{
			name:   "indicator only",
			state:  render.ViewState{Loading: true, Spinner: "|", Status: "Working"},
			tokens: []string{"|", "Working"},
		},
		{
			name:   "indicator with a reasoning trace",
			state:  render.ViewState{Loading: true, Spinner: "|", Status: "Working", Reasoning: render.Reasoning{Text: "thinking about it"}},
			tokens: []string{"|", "Working", "thinking about it"},
		},
		{
			// Pins CollapsibleBox's tail-anchoring end to end through BOTH
			// presenters, not just inline (which tui/reasoning_test.go already
			// covers via the model). Without this case, full's own collapsing
			// branch was invoked by no test in the suite: TestReasoningStruc-
			// turalEquivalence never set Collapsed/MaxLines, so it only ever
			// exercised the pass-through (uncapped) path, and full.Reasoning
			// could regress independently of inline with nothing to catch it.
			name: "collapsed reasoning trace tails, does not head",
			state: render.ViewState{
				Loading: true, Spinner: "|", Status: "Working",
				Reasoning: render.Reasoning{Text: collapsedTrace, Collapsed: true, MaxLines: 3},
			},
			tokens: []string{"|", "Working", "line-6", "line-7", "line-8", wantHint},
			absent: []string{"line-1", "line-2", "line-3", "line-4", "line-5"},
		},
		{
			// Base.Reasoning's r.Collapsed && box.Hidden() == 0 branch: a short
			// trace that never grew past MaxLines has nothing collapsing would
			// change, so neither the expand nor the collapse invitation makes
			// sense — the hint is suppressed entirely rather than shown as one
			// or the other. Nothing exercised this branch through either
			// presenter before this case.
			name: "collapsed reasoning trace with nothing hidden shows no hint",
			state: render.ViewState{
				Loading: true, Spinner: "|", Status: "Working",
				Reasoning: render.Reasoning{Text: "short", Collapsed: true, MaxLines: 20},
			},
			tokens: []string{"|", "Working", "short"},
			absent: []string{theme.ReasoningExpand, theme.ReasoningCollapse},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fps := map[string]fingerprint{}
			for name, p := range presenters() {
				out := p.Reasoning(tc.state, w)
				assertPreserves(t, name, out, tc.tokens)
				assertFitsWidth(t, name, out, w)
				for _, tok := range tc.absent {
					if strings.Contains(stripANSI(out), tok) {
						t.Errorf("%s collapsed box leaked hidden content %q: %q", name, tok, out)
					}
				}
				fps[name] = fingerprintOf(out, tc.tokens)
			}
			assertSameFingerprint(t, fps)
		})
	}
}

// footerStates enumerates the footer shapes that actually occur, from the bare
// help line to every row at once. Shared by the structural check and the
// FooterHeight budget check below.
func footerStates() []struct {
	name  string
	state render.ViewState
} {
	help := "tab complete"
	rows := []render.FooterRow{
		{Glyph: "*", Text: "2 jobs running", Kind: render.FooterJobs},
		{Glyph: ">", Text: "1 shell job", Kind: render.FooterShell},
		{Glyph: "~", Text: "loop every 5m", Kind: render.FooterLoops},
		{Glyph: "o", Text: "goal: ship it", Kind: render.FooterGoal},
	}
	return []struct {
		name  string
		state render.ViewState
	}{
		{"help only", render.ViewState{Help: help}},
		{"help and badges", render.ViewState{Help: help, Badges: "12k ctx"}},
		{"new output marker", render.ViewState{Help: help, NewOutput: true}},
		{"error line", render.ViewState{Help: help, Err: "something failed"}},
		{"one job row", render.ViewState{Help: help, Footers: rows[:1]}},
		{"job and loop rows", render.ViewState{Help: help, Footers: []render.FooterRow{rows[0], rows[2]}}},
		{"every row", render.ViewState{Help: help, Badges: "12k ctx", NewOutput: true, Err: "something failed", Footers: rows}},
		{"expanded telemetry", render.ViewState{Help: help, HelpRight: "ctrl+g activity", Badges: "12k ctx", Expanded: "12:00:00", Footers: rows}},
		{"a row with no glyph", render.ViewState{Help: help, Footers: []render.FooterRow{{Text: "unset kind row"}}}},
	}
}

// TestFooterStructuralEquivalence: the footer's rows are domain data, so both
// surfaces must render the same number of them, in the same order, with the
// same text — however differently they style them.
func TestFooterStructuralEquivalence(t *testing.T) {
	// Wide enough that no chip label is shortened to fit.
	const w = 100
	for _, tc := range footerStates() {
		t.Run(tc.name, func(t *testing.T) {
			var tokens []string
			if tc.state.Help != "" {
				tokens = append(tokens, tc.state.Help)
			}
			if tc.state.Badges != "" {
				tokens = append(tokens, tc.state.Badges)
			}
			if tc.state.Expanded != "" {
				tokens = append(tokens, tc.state.Expanded)
			}
			if tc.state.HelpRight != "" {
				tokens = append(tokens, tc.state.HelpRight)
			}
			if tc.state.Err != "" {
				tokens = append(tokens, tc.state.Err)
			}
			for _, row := range tc.state.Footers {
				tokens = append(tokens, row.Text)
			}
			fps := map[string]fingerprint{}
			for name, p := range presenters() {
				out := p.Footer(tc.state, w)
				assertPreserves(t, name, out, tokens)
				fps[name] = fingerprintOf(out, tokens)
			}
			assertSameFingerprint(t, fps)
		})
	}
}

// TestFooterHeightMatchesFooter is the height-budget guard. The core subtracts
// FooterHeight from the frame to size the viewport, so an answer that disagrees
// with Footer by even one row makes an over-tall frame — which on the alt
// screen scrolls the header off the top every time a job row appears.
//
// The expectation is counted off the FIXTURE, not measured from Footer: the
// help line, plus a row for the new-output marker, plus a row for the error
// line, plus one per job-status row. It used to be
// lipgloss.Height(p.Footer(...)), which is character for character the body of
// both FooterHeight implementations — a tautology that could not fail for any
// change to either footer, including one that dropped a row entirely. Deriving
// it here means the fixture, not the implementation, says how tall a footer of
// this shape is; a presenter that starts wrapping its help line or stacking
// badges onto a second row has to come and update this count, which is exactly
// the conversation the layout budget needs to have.
func TestFooterHeightMatchesFooter(t *testing.T) {
	// w = 20 is deliberately narrow, but every fixture's help/badges/error
	// text is short enough that no presenter wraps it there, so the row count
	// below holds at both widths.
	for _, w := range []int{20, 60} {
		for _, tc := range footerStates() {
			want := 2 // summary and help lines, always present
			if tc.state.NewOutput {
				want++
			}
			// The front telemetry shares the help line.
			if tc.state.Expanded != "" {
				want++
			}
			if len(tc.state.Footers) > 0 {
				want++ // every chip shares the activity strip's one line
			}
			if tc.state.Err != "" {
				want++
			}

			for name, p := range presenters() {
				if got := p.FooterHeight(tc.state, w); got != want {
					t.Errorf("%s FooterHeight(%s, w=%d) = %d, want %d rows: %q",
						name, tc.name, w, got, want, p.Footer(tc.state, w))
				}
				// And Footer itself must actually emit that many, or the
				// budget is right about the wrong string.
				if got := lipgloss.Height(p.Footer(tc.state, w)); got != want {
					t.Errorf("%s Footer(%s, w=%d) emits %d rows, want %d: %q",
						name, tc.name, w, got, want, p.Footer(tc.state, w))
				}
			}
		}
	}
}

// TestHeaderHeightMatchesHeader is the other half of the layout budget. The
// core subtracts HeaderHeight from the frame to size the viewport, and
// concatenates body straight onto the header string, so the answer must be the
// number of rows the header leaves above the body — not lipgloss.Height, which
// counts the empty piece after a trailing newline as a row of its own.
//
// The expectation is derived from the fixture rather than from Header: the
// header is the brand/cwd line and the hairline beneath it, so two rows, and
// the body starts on the third.
func TestHeaderHeightMatchesHeader(t *testing.T) {
	v := render.ViewState{Width: 60, Brand: "nib", Cwd: "~/src/project"}
	const wantRows = 2 // brand/cwd line + hairline
	for name, p := range presenters() {
		if got := p.HeaderHeight(v); got != wantRows {
			t.Errorf("%s HeaderHeight = %d, want %d (brand line + hairline)", name, got, wantRows)
		}
		// And the body really does start on the row after those two: the frame
		// writes body directly onto the header with no separator.
		out := p.Frame(v, p.Header(v), "BODY", "COMPOSER", p.Footer(v, 60), 60, 24)
		lines := strings.Split(out, "\n")
		if len(lines) <= wantRows || !strings.HasPrefix(lines[wantRows], "BODY") {
			t.Errorf("%s: body does not start on row %d of the frame: %q", name, wantRows, out)
		}
	}
}

// TestFooterHeightGrowsWithRows pins the actual bug this answers: a fixed
// budget of 3 was wrong because the footer is not a fixed height. A session
// with a running sub-agent and a live loop must report more rows than a bare
// help line.
func TestFooterHeightGrowsWithRows(t *testing.T) {
	const w = 60
	bare := render.ViewState{Help: "tab complete"}
	busy := render.ViewState{
		Help:      "tab complete",
		NewOutput: true,
		Err:       "something failed",
		Footers: []render.FooterRow{
			{Glyph: "*", Text: "2 jobs running", Kind: render.FooterJobs},
			{Glyph: "~", Text: "loop every 5m", Kind: render.FooterLoops},
		},
	}
	for name, p := range presenters() {
		lo, hi := p.FooterHeight(bare, w), p.FooterHeight(busy, w)
		if lo != 2 {
			t.Errorf("%s FooterHeight(bare) = %d, want 2", name, lo)
		}
		if hi != 5 {
			t.Errorf("%s FooterHeight(busy) = %d, want 5 (summary + marker + activity strip + help + error)", name, hi)
		}
	}
}

// TestContentWidthLeavesRoomForChrome: every presenter must report a content
// width that its own Message output actually respects, and never a width below
// 1 (glamour and Wrap both need a legal budget even on a terminal narrower
// than the chrome).
func TestContentWidthLeavesRoomForChrome(t *testing.T) {
	roles := []render.Role{render.RoleUser, render.RoleAssistant, render.RoleAgent, render.RoleTool, render.RoleError, render.RoleNone}
	for name, p := range presenters() {
		for _, role := range roles {
			for _, w := range []int{1, 3, 40, 200} {
				got := p.ContentWidth(role, w)
				if got < 1 {
					t.Errorf("%s ContentWidth(%q, %d) = %d, want at least 1", name, role, w, got)
				}
				if got > w {
					t.Errorf("%s ContentWidth(%q, %d) = %d, wider than the frame", name, role, w, got)
				}
			}
		}
	}
}

// TestContentWidthMatchesRenderedPrefix ties ContentWidth to what Message
// actually lays out: content rendered at the reported width must fit the frame
// once the presenter has added its own chrome. This is the property the model
// relies on when it pre-renders markdown (I2) instead of reconstructing one
// surface's prefix for both.
//
// prev is varied for RoleUser/RoleAssistant because Phase 3 Task 12 made
// inline's prefix shape depend on it (the label on the first message of a
// run, an all-spaces prefix of the SAME width on a consecutive one) while
// ContentWidth stays role-only (see its doc — it cannot see prev at all).
// That is only a safe design if both prefix shapes are really the same
// width; this loop is what would catch it if they ever drifted apart — a
// content string sized to ContentWidth(role, w) would then overflow w under
// whichever prefix ContentWidth did NOT measure.
func TestContentWidthMatchesRenderedPrefix(t *testing.T) {
	const w = 48
	for name, p := range presenters() {
		for _, role := range []render.Role{render.RoleUser, render.RoleAssistant, render.RoleAgent, render.RoleError} {
			cw := p.ContentWidth(role, w)
			content := strings.Repeat("x", cw)

			prevs := []render.Role{render.RoleNone}
			if role == render.RoleUser || role == render.RoleAssistant {
				prevs = append(prevs, role)
			}
			for _, prev := range prevs {
				out := p.Message(render.Message{Role: role, Content: content, Label: "label"}, prev, w)
				assertFitsWidth(t, name+"/"+string(role)+"/prev="+string(prev), out, w)
			}
		}
	}
}

// TestFrameContainsEveryPiece is Task 10a's composition-point guard: every
// block a frame is made of — the header's brand/cwd, the body (the rendered
// transcript viewport, standing in here for whatever Messages/Reasoning/
// Dialogs produced it), the composer, and the footer's help/badges/error/job
// rows — must survive into Frame's output on both surfaces. This is what
// closes the review finding that no Presenter method received a height and
// no method could see body/composer/footer/chrome all at once.
//
// It does NOT assert an (w, h) clamp, and no later task added one: neither
// presenter clamps to h — both stack, exactly as they did before Frame
// existed, so a body taller than h passes through unclamped on both surfaces.
// Fitting the frame to the terminal is the CORE's job, not a presenter's: the
// core sizes the viewport so that header + body + dialogs + composer + footer
// add up to h (see tui/model.go's layoutBudget and TestFrameFitsTheTerminal,
// which measures the composed frame against the terminal on both surfaces,
// dialogs included). A clamp here would be a second, silent answer to the same
// question, and would hide a budget that was wrong by truncating it. The note
// that used to sit here deferred the whole question to a task that never took
// it up, and a ten-row approval card overflowing the alt screen by ten rows is
// what that cost.
func TestFrameContainsEveryPiece(t *testing.T) {
	const w, h = 60, 40
	v := render.ViewState{
		Width: w,
		Brand: "nib", Cwd: "~/src/project",
		Help: "tab complete", Badges: "12k ctx", Err: "something failed",
		Footers: []render.FooterRow{{Glyph: "*", Text: "1 job running", Kind: render.FooterJobs}},
	}
	body := "BODY-MARKER-ONE\nBODY-MARKER-TWO"
	composer := "COMPOSER-MARKER"

	for name, p := range presenters() {
		t.Run(name, func(t *testing.T) {
			header := p.Header(v)
			footer := p.Footer(v, w)
			out := p.Frame(v, header, body, composer, footer, w, h)

			tokens := []string{
				"nib", "~/src/project", // header
				"BODY-MARKER-ONE", "BODY-MARKER-TWO", // body
				"COMPOSER-MARKER",                                              // composer
				"tab complete", "12k ctx", "something failed", "1 job running", // footer
			}
			assertPreserves(t, name, out, tokens)

			if !strings.Contains(out, composer) {
				t.Errorf("%s Frame dropped the composer entirely: %q", name, out)
			}
		})
	}
}

// TestFrameStructuralEquivalence: both surfaces must agree on how the four
// pieces relate to each other structurally (how many blocks, in what order),
// even though Task 10a deliberately keeps both stacking rather than framing —
// the point of this task is that the restructuring is behaviour-preserving on
// both surfaces, provably so via the same fingerprint comparison the rest of
// this suite uses.
func TestFrameStructuralEquivalence(t *testing.T) {
	const w, h = 60, 40
	v := render.ViewState{Width: w, Brand: "nib", Cwd: "~/src/project", Help: "tab complete"}
	body := "BODY-MARKER"
	composer := "COMPOSER-MARKER"
	tokens := []string{"nib", "~/src/project", "BODY-MARKER", "COMPOSER-MARKER", "tab complete"}

	fps := map[string]fingerprint{}
	for name, p := range presenters() {
		header := p.Header(v)
		footer := p.Footer(v, w)
		out := p.Frame(v, header, body, composer, footer, w, h)
		fps[name] = fingerprintOf(out, tokens)
	}
	assertSameFingerprint(t, fps)
}

// dialogLine returns the first line of out that contains text, or "" if none
// does. Shared by the selection- and check-marking conformance tests below.
func dialogLine(out, text string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, text) {
			return l
		}
	}
	return ""
}

// TestAllPresentersMarkDialogSelection: the selected option must be visually
// distinguishable in every surface, however each one chooses to mark it — and
// the mark must track Selected, not a fixed row. Moved here from Phase 2 Task
// 7 by controller ruling: it asserts behaviour that only exists once Phase 3
// Task 11 (the ask_user dialog) lands.
//
// The first half (Selected: 1) only proves SOME difference exists between a
// selected and an unselected line, which a presenter that hardcoded
// `i == 0` instead of `i == d.Selected` would also pass (0 happens to differ
// from 1). The second half closes that gap: it re-renders the same three
// options with Selected moved to 0 and then to 2, and checks that alpha's OWN
// line and gamma's OWN line each change between those two renders. A
// presenter that always marks row 0 regardless of Selected would render
// alpha's line identically both times (always marked) and gamma's line
// identically both times (never marked) — this is what would fail.
func TestAllPresentersMarkDialogSelection(t *testing.T) {
	dialogWith := func(selected int) render.Dialog {
		return render.Dialog{
			Kind:  render.DialogAsk,
			Title: "pick one",
			// Options is []render.DialogOption{Text, Emphasis} — the explicit shape
			// adopted in Phase 2 Task 6's fix round, replacing positional styling.
			Options: []render.DialogOption{
				{Text: "alpha"}, {Text: "beta"}, {Text: "gamma"},
			},
			Selected: selected,
		}
	}
	for name, p := range presenters() {
		t.Run(name, func(t *testing.T) {
			out1 := p.Dialog(dialogWith(1), 80)
			alpha1, beta1, gamma1 := dialogLine(out1, "alpha"), dialogLine(out1, "beta"), dialogLine(out1, "gamma")
			if alpha1 == "" || beta1 == "" || gamma1 == "" {
				t.Fatalf("%s did not render all options: %q", name, out1)
			}
			if beta1 == strings.Replace(alpha1, "alpha", "beta", 1) {
				t.Errorf("%s renders the selected option identically to an unselected one: %q", name, beta1)
			}

			out0 := p.Dialog(dialogWith(0), 80)
			out2 := p.Dialog(dialogWith(2), 80)
			alpha0, gamma0 := dialogLine(out0, "alpha"), dialogLine(out0, "gamma")
			alpha2, gamma2 := dialogLine(out2, "alpha"), dialogLine(out2, "gamma")
			if alpha0 == "" || gamma0 == "" || alpha2 == "" || gamma2 == "" {
				t.Fatalf("%s did not render all options at every Selected: Selected=0: %q, Selected=2: %q", name, out0, out2)
			}
			if alpha0 == alpha2 {
				t.Errorf("%s renders alpha's row identically whether Selected is 0 or 2 — the mark is not tracking Selected: %q", name, alpha0)
			}
			if gamma0 == gamma2 {
				t.Errorf("%s renders gamma's row identically whether Selected is 0 or 2 — the mark is not tracking Selected: %q", name, gamma0)
			}
		})
	}
}

// TestAllPresentersRenderMultiSelectChecks: a Dialog with Checked must show
// which options are ticked on every surface. This is the only fixture in the
// whole conformance suite that sets Checked — without it, a presenter's
// checkbox-glyph path (as opposed to its radio path) has no coverage on
// `full` at all: tui/ask_test.go's multi-select assertions run only against
// `inline` (testPresenter() is hard-wired to inline.New()).
func TestAllPresentersRenderMultiSelectChecks(t *testing.T) {
	d := render.Dialog{
		Kind:  render.DialogAsk,
		Title: "pick some",
		Options: []render.DialogOption{
			{Text: "red"}, {Text: "green"}, {Text: "blue"},
		},
		Checked: []bool{true, false, true},
		// Selected is deliberately out of range (Dialog's own zero value, 0,
		// would coincide with "red" at index 0 and add a cursor mark that
		// "blue" doesn't get, confounding the checked-vs-checked comparison
		// below with an unrelated Selected difference).
		Selected: -1,
	}
	for name, p := range presenters() {
		t.Run(name, func(t *testing.T) {
			out := p.Dialog(d, 80)
			redLine, greenLine, blueLine := dialogLine(out, "red"), dialogLine(out, "green"), dialogLine(out, "blue")
			if redLine == "" || greenLine == "" || blueLine == "" {
				t.Fatalf("%s did not render all options: %q", name, out)
			}
			// A checked row's own line must look different from an unchecked
			// one — not merely different text — same discriminator
			// TestAllPresentersMarkDialogSelection uses for Selected.
			if redLine == strings.Replace(greenLine, "green", "red", 1) {
				t.Errorf("%s renders a checked option identically to an unchecked one: %q", name, redLine)
			}
			if blueLine == strings.Replace(greenLine, "green", "blue", 1) {
				t.Errorf("%s renders a checked option identically to an unchecked one: %q", name, blueLine)
			}
			// red and blue are both checked and neither is Selected (the zero
			// value): their glyph should read the same, so this catches a
			// presenter whose mark happens to differ by position rather than
			// by Checked[i].
			if redLine != strings.Replace(blueLine, "blue", "red", 1) {
				t.Errorf("%s renders two checked options differently from each other: %q vs %q", name, redLine, blueLine)
			}
		})
	}
}

// TestFrameOverlaysDialogsOnlyWhereDeclared: Phase 3 Task 11 lets `full` place
// v.Dialogs itself, as a real overlay, instead of relying on them having been
// baked into body — this is the one place inline and full are SUPPOSED to
// diverge (chrome: WHERE a dialog reaches the screen), and this test is the
// guard that the divergence is exactly this and nothing more.
//
// A presenter that declares Caps.OverlayDialogs (full) must place v.Dialogs
// somewhere in Frame's output — body here deliberately carries none of the
// dialog's own text, so if Frame doesn't do it, nobody does, and the ask
// dialog silently vanishes on that surface. A presenter that does NOT declare
// it (inline) must NOT also render one from Frame: production always bakes it
// into body first (updateViewport, tui/model.go) for that surface, so a
// second render here would be an outright duplicate on screen. Both body and
// composer must still survive regardless — the divergence is additive, not a
// replacement for the rest of Frame's job (see TestFrameContainsEveryPiece).
func TestFrameOverlaysDialogsOnlyWhereDeclared(t *testing.T) {
	const w, h = 60, 40
	v := render.ViewState{
		Width: w, Brand: "nib", Cwd: "~/src/project",
		Dialogs: []render.Dialog{{
			Kind:    render.DialogAsk,
			Title:   "OVERLAY-MARKER-QUESTION",
			Options: []render.DialogOption{{Text: "OVERLAY-MARKER-OPTION"}},
		}},
	}
	body := "BODY-ONLY-CONTENT"
	composer := "COMPOSER-MARKER"

	for name, p := range presenters() {
		t.Run(name, func(t *testing.T) {
			header := p.Header(v)
			footer := p.Footer(v, w)
			out := p.Frame(v, header, body, composer, footer, w, h)

			assertPreserves(t, name, out, []string{"BODY-ONLY-CONTENT", "COMPOSER-MARKER"})

			hasDialog := strings.Contains(out, "OVERLAY-MARKER-QUESTION") || strings.Contains(out, "OVERLAY-MARKER-OPTION")
			switch overlays := p.Caps().OverlayDialogs; {
			case overlays && !hasDialog:
				t.Errorf("%s declares OverlayDialogs but Frame did not place v.Dialogs: %q", name, out)
			case !overlays && hasDialog:
				t.Errorf("%s does not declare OverlayDialogs, but Frame rendered v.Dialogs anyway — production already bakes it into body for this surface, so this would double it: %q", name, out)
			}
		})
	}
}
