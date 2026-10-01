package render

import (
	"regexp"
	"strings"
	"testing"
)

var baseTestSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// TestBaseContentWidthUsesInjectedPrefix pins the embedding fix Task 17
// depends on: Base cannot call back into an embedder's own contentPrefix (Go
// embedding has no virtual dispatch), so ContentWidth must measure whatever
// function was actually stored in the Prefix field, not some default of its
// own.
func TestBaseContentWidthUsesInjectedPrefix(t *testing.T) {
	b := Base{Prefix: func(Role) string { return "XXXX" }} // 4 cells
	if got, want := b.ContentWidth(RoleUser, 10), 6; got != want {
		t.Errorf("ContentWidth = %d, want %d (10 - 4-cell prefix)", got, want)
	}
}

// TestBaseContentWidthClampsToAtLeastOne: a terminal narrower than the chrome
// must still yield a legal width for a renderer (glamour, Wrap) to use.
func TestBaseContentWidthClampsToAtLeastOne(t *testing.T) {
	b := Base{Prefix: func(Role) string { return "far too wide a prefix" }}
	if got := b.ContentWidth(RoleUser, 5); got != 1 {
		t.Errorf("ContentWidth = %d, want 1 (clamped)", got)
	}
}

// TestBaseContentWidthPanicsWithoutPrefix pins the loud-failure contract: a
// future third presenter that embeds Base and forgets to wire up Prefix must
// get a clear panic naming the cause, not a bare nil-func dereference and
// never a silent default that would render the wrong chrome.
func TestBaseContentWidthPanicsWithoutPrefix(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("ContentWidth with a nil Prefix did not panic")
		}
		if msg, ok := r.(string); !ok || !strings.Contains(msg, "Prefix") {
			t.Errorf("panic value = %v, want a message naming Prefix", r)
		}
	}()
	Base{}.ContentWidth(RoleUser, 10)
}

// TestBaseHeaderHeightMatchesHeader ties HeaderHeight to Header's own output
// directly on Base, independent of either presenter: the layout budget
// subtracts HeaderHeight from the frame, so an answer that disagrees with the
// real string over- or under-budgets the viewport.
func TestBaseHeaderHeightMatchesHeader(t *testing.T) {
	b := Base{}
	v := ViewState{Width: 60, Brand: "nib", Cwd: "~/src/project"}
	if got, want := b.HeaderHeight(v), BlockRows(b.Header(v)); got != want {
		t.Errorf("HeaderHeight = %d, want %d (BlockRows of the real Header output)", got, want)
	}
}

// TestBaseHeaderNamesTheProvider: the header shows "provider · model" so a
// /login pick is never mistaken for config.yaml's model, and on a terminal
// too narrow for both the provider gives way before the model does.
func TestBaseHeaderNamesTheProvider(t *testing.T) {
	b := Base{}
	v := ViewState{Width: 80, Brand: "nib", Cwd: "~/p",
		HeaderStats: HeaderStats{Provider: "regolo", Model: "glm5.2"}}
	line := baseTestSGR.ReplaceAllString(strings.SplitN(b.Header(v), "\n", 2)[0], "")
	if !strings.Contains(line, "regolo · glm5.2") {
		t.Fatalf("header = %q, want %q", line, "regolo · glm5.2")
	}

	v.Width = 22 // room for brand, model and cwd, not the provider
	line = baseTestSGR.ReplaceAllString(strings.SplitN(b.Header(v), "\n", 2)[0], "")
	if strings.Contains(line, "regolo") || !strings.Contains(line, "glm5.2") {
		t.Fatalf("narrow header = %q, want the model without the provider", line)
	}
}

// TestBaseFooterHeightMatchesFooter is the same guard for the footer: the
// core measures FooterHeight before body is laid out, so it must count
// exactly the rows Footer itself renders (lipgloss.Height, since — unlike
// Header — nothing is written directly onto Footer's own trailing content).
func TestBaseFooterHeightMatchesFooter(t *testing.T) {
	b := Base{}
	v := ViewState{
		Help: "tab complete", Badges: "12k ctx", NewOutput: true, Err: "boom",
		Footers: []FooterRow{{Glyph: "*", Text: "1 job running", Kind: FooterJobs}},
	}
	const w = 60
	got := b.FooterHeight(v, w)
	want := 5 // summary + marker + activity strip + help (with the badges) + error
	if got != want {
		t.Errorf("FooterHeight = %d, want %d", got, want)
	}
}

// TestBaseDialogUnhandledKindRendersNothing pins the shared contract
// TestUnknownDialogKindRendersNothing (conformance_test.go) exercises through
// both presenters: a Kind neither ask/resume nor approval must render as
// nothing, not a half-drawn card.
func TestBaseDialogUnhandledKindRendersNothing(t *testing.T) {
	b := Base{}
	if out := b.Dialog(Dialog{Kind: DialogKind(99), Title: "resume?"}, 60); out != "" {
		t.Errorf("Dialog(unhandled kind) = %q, want empty", out)
	}
}

// TestBaseReasoningRendersNothingWhenIdle pins the same not-loading contract
// directly on Base, since it is the sole implementation both presenters now
// share.
func TestBaseReasoningRendersNothingWhenIdle(t *testing.T) {
	b := Base{}
	v := ViewState{Loading: false, Spinner: "|", Status: "Working", Reasoning: Reasoning{Text: "thinking"}}
	if out := b.Reasoning(v, 50); out != "" {
		t.Errorf("Reasoning(idle) = %q, want empty", out)
	}
}
