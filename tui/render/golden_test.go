package render_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
)

// updateGolden regenerates the fixtures under testdata/golden. It exists so a
// deliberate, reviewed change to a presenter's rendered output can update its
// safety net in one step — Task 17's extraction itself must never need it: a
// failure during that refactor means the extraction moved a byte, and the fix
// is to correct the extraction, never to run this flag and re-capture the new
// (wrong) output as the new golden.
var updateGolden = flag.Bool("golden.update", false, "regenerate tui/render/testdata/golden fixtures")

// goldenCase is one named probe of a Presenter method. fn must be a pure
// function of p and the literal fixtures closed over in goldenCases — no
// randomness, no wall-clock, no map iteration — so the same presenter always
// produces the same golden text.
type goldenCase struct {
	name string
	fn   func(p render.Presenter) string
}

// goldenCases is Task 17's pre-extraction safety net: a representative spread
// of ViewStates/Messages/Dialogs that, between the two of them, touches every
// method of the Presenter interface at least once, including:
//
//   - every Role Message renders (user, assistant, agent, agent with HugNext,
//     tool, tool from a sub-agent, error, an unknown role) plus a consecutive
//     same-role pair for user AND assistant (the only two roles whose prefix
//     varies by prev) and a multi-paragraph body;
//   - ContentWidth for every role, including the sub-1-cell clamp;
//   - Reasoning idle, with only the working indicator, with an expanded trace,
//     with a trace collapsed enough to hide lines, and collapsed with nothing
//     hidden (a short trace that never grew past MaxLines, which suppresses
//     the hint line entirely);
//   - Dialog's every DialogKind (Approval in its five-option/edit-mode/
//     unstructured/no-option/two-option shapes, Ask, Ask with multi-select
//     checks, Resume, and an unhandled kind);
//   - Header plain and with the yolo badge, and HeaderHeight;
//   - Footer for every FooterRowKind plus the bare help line, badges, the
//     new-output marker, the error line, and every row at once, and
//     FooterHeight for the busiest of those;
//   - Frame with no pending dialog and with one, so `full`'s overlay and
//     `inline`'s stack both leave a mark here;
//   - Caps.
//
// Committed once, green against the pre-Task-17 inline/full, this is the
// evidence that the extraction changed no rendered byte: TestGolden replays
// every case against the post-extraction presenters and diffs the result
// against the exact same fixture file.
func goldenCases() []goldenCase {
	approvalRows := [][2]string{{"path", "main.go"}, {"mode", "0644"}}
	fiveOptionMenu := []render.DialogOption{
		{Text: theme.ApproveOnce, Emphasis: true},
		{Text: theme.ApproveAlwaysPrefix + "`touch …`" + theme.ApproveAlwaysSuffix, Emphasis: true},
		{Text: theme.ApproveTurn, Emphasis: true},
		{Text: theme.ApproveSession, Emphasis: true},
		{Text: theme.ApproveDenyEdit, Emphasis: false},
	}
	collapsedTrace := "line-1\nline-2\nline-3\nline-4\nline-5\nline-6\nline-7\nline-8"

	headerState := render.ViewState{Width: 60, Brand: "nib", Cwd: "~/src/project"}
	busyFooter := render.ViewState{
		Help:      "tab complete",
		Badges:    "12k ctx",
		NewOutput: true,
		Err:       "something failed",
		Footers: []render.FooterRow{
			{Glyph: "*", Text: "2 jobs running", Kind: render.FooterJobs},
			{Glyph: ">", Text: "1 shell job", Kind: render.FooterShell},
			{Glyph: "~", Text: "loop every 5m", Kind: render.FooterLoops},
			{Glyph: "o", Text: "goal: ship it", Kind: render.FooterGoal},
		},
	}

	const w = 60

	return []goldenCase{
		{"caps", func(p render.Presenter) string { return fmt.Sprintf("%+v", p.Caps()) }},

		{"header plain", func(p render.Presenter) string { return p.Header(headerState) }},
		{"header with yolo badge", func(p render.Presenter) string {
			v := headerState
			v.AutoApprove = true
			return p.Header(v)
		}},
		{"header height", func(p render.Presenter) string { return fmt.Sprintf("%d", p.HeaderHeight(headerState)) }},

		{"content width user", func(p render.Presenter) string { return fmt.Sprintf("%d", p.ContentWidth(render.RoleUser, w)) }},
		{"content width assistant", func(p render.Presenter) string { return fmt.Sprintf("%d", p.ContentWidth(render.RoleAssistant, w)) }},
		{"content width agent", func(p render.Presenter) string { return fmt.Sprintf("%d", p.ContentWidth(render.RoleAgent, w)) }},
		{"content width tool", func(p render.Presenter) string { return fmt.Sprintf("%d", p.ContentWidth(render.RoleTool, w)) }},
		{"content width error", func(p render.Presenter) string { return fmt.Sprintf("%d", p.ContentWidth(render.RoleError, w)) }},
		{"content width narrow clamp", func(p render.Presenter) string { return fmt.Sprintf("%d", p.ContentWidth(render.RoleUser, 1)) }},

		{"message user first", func(p render.Presenter) string {
			return p.Message(render.Message{Role: render.RoleUser, Content: "hello there"}, render.RoleNone, w)
		}},
		{"message user consecutive", func(p render.Presenter) string {
			return p.Message(render.Message{Role: render.RoleUser, Content: "second turn"}, render.RoleUser, w)
		}},
		{"message assistant first", func(p render.Presenter) string {
			return p.Message(render.Message{Role: render.RoleAssistant, Content: "assistant answer"}, render.RoleNone, w)
		}},
		{"message assistant consecutive", func(p render.Presenter) string {
			return p.Message(render.Message{Role: render.RoleAssistant, Content: "second answer"}, render.RoleAssistant, w)
		}},
		{"message agent", func(p render.Presenter) string {
			return p.Message(render.Message{Role: render.RoleAgent, Content: "agent started", AgentID: "abcdef123456"}, render.RoleNone, w)
		}},
		{"message agent hugnext", func(p render.Presenter) string {
			return p.Message(render.Message{Role: render.RoleAgent, Content: "agent started", AgentID: "abcdef123456", HugNext: true}, render.RoleNone, w)
		}},
		{"message tool", func(p render.Presenter) string {
			return p.Message(render.Message{Role: render.RoleTool, Label: "read file.go", Content: "tool output body"}, render.RoleNone, w)
		}},
		{"message tool from sub-agent", func(p render.Presenter) string {
			return p.Message(render.Message{Role: render.RoleTool, Label: "read file.go", Content: "tool output body", AgentID: "abcdef123456"}, render.RoleNone, w)
		}},
		{"message error", func(p render.Presenter) string {
			return p.Message(render.Message{Role: render.RoleError, Content: "it went wrong"}, render.RoleNone, w)
		}},
		{"message multiline assistant", func(p render.Presenter) string {
			return p.Message(render.Message{Role: render.RoleAssistant, Content: "first para\n\nsecond para"}, render.RoleNone, w)
		}},
		{"message unknown role", func(p render.Presenter) string {
			return p.Message(render.Message{Role: render.RoleNone, Content: "orphan"}, render.RoleNone, w)
		}},

		{"reasoning idle", func(p render.Presenter) string {
			return p.Reasoning(render.ViewState{Loading: false, Spinner: "|", Status: "Working", Reasoning: render.Reasoning{Rendered: "thinking about it"}}, 50)
		}},
		{"reasoning indicator only", func(p render.Presenter) string {
			return p.Reasoning(render.ViewState{Loading: true, Spinner: "|", Status: "Working"}, 50)
		}},
		{"reasoning expanded with trace", func(p render.Presenter) string {
			return p.Reasoning(render.ViewState{Loading: true, Spinner: "|", Status: "Working", Reasoning: render.Reasoning{Rendered: "thinking about it"}}, 50)
		}},
		{"reasoning collapsed with hidden lines", func(p render.Presenter) string {
			return p.Reasoning(render.ViewState{
				Loading: true, Spinner: "|", Status: "Working",
				Reasoning: render.Reasoning{Rendered: collapsedTrace, Collapsed: true, MaxLines: 3},
			}, 50)
		}},
		{"reasoning collapsed with nothing hidden", func(p render.Presenter) string {
			return p.Reasoning(render.ViewState{
				Loading: true, Spinner: "|", Status: "Working",
				Reasoning: render.Reasoning{Rendered: "short", Collapsed: true, MaxLines: 20},
			}, 50)
		}},

		{"dialog approval five option", func(p render.Presenter) string {
			return p.Dialog(render.Dialog{
				Kind: render.DialogApproval, Title: "bash wants to run",
				Rows: approvalRows, Hint: "needs the fix applied", Options: fiveOptionMenu,
			}, w)
		}},
		{"dialog approval edit mode", func(p render.Presenter) string {
			return p.Dialog(render.Dialog{
				Kind: render.DialogApproval, Title: "write main.go", Rows: approvalRows,
				Options: []render.DialogOption{{Text: "enter to send", Emphasis: true}},
			}, w)
		}},
		{"dialog approval unstructured", func(p render.Presenter) string {
			return p.Dialog(render.Dialog{
				Kind: render.DialogApproval, Title: "run something",
				Rows:             [][2]string{{"", "a raw prose block describing the call"}},
				RowsUnstructured: true,
				Options:          []render.DialogOption{{Text: "[y] yes", Emphasis: true}},
			}, w)
		}},
		{"dialog approval no options", func(p render.Presenter) string {
			return p.Dialog(render.Dialog{Kind: render.DialogApproval, Title: "write main.go", Rows: approvalRows}, w)
		}},
		{"dialog approval two option menu", func(p render.Presenter) string {
			return p.Dialog(render.Dialog{
				Kind: render.DialogApproval, Title: "write main.go",
				Options: []render.DialogOption{{Text: "[y] yes", Emphasis: true}, {Text: "[n] no", Emphasis: false}},
			}, w)
		}},
		{"dialog ask", func(p render.Presenter) string {
			return p.Dialog(render.Dialog{
				Kind: render.DialogAsk, Title: "which one?",
				Options:  []render.DialogOption{{Text: "alpha"}, {Text: "beta"}},
				Selected: 1, Hint: "type instead of picking also works",
			}, w)
		}},
		{"dialog ask multiselect", func(p render.Presenter) string {
			return p.Dialog(render.Dialog{
				Kind: render.DialogAsk, Title: "pick some",
				Options: []render.DialogOption{{Text: "red"}, {Text: "green"}, {Text: "blue"}},
				Checked: []bool{true, false, true}, Selected: -1,
			}, w)
		}},
		{"dialog resume", func(p render.Presenter) string {
			return p.Dialog(render.Dialog{
				Kind: render.DialogResume, Title: "resume a session",
				Options: []render.DialogOption{
					{Text: "fix the bug · 3m ago · 4 messages"},
					{Text: "add tests · 1h ago · 2 messages"},
				},
				Selected: 1, Hint: "up/down move · enter resume · esc cancel",
			}, w)
		}},
		{"dialog unknown kind", func(p render.Presenter) string {
			return p.Dialog(render.Dialog{Kind: render.DialogKind(99), Title: "resume?"}, w)
		}},

		{"footer help only", func(p render.Presenter) string { return p.Footer(render.ViewState{Help: "tab complete"}, w) }},
		{"footer help and badges", func(p render.Presenter) string {
			return p.Footer(render.ViewState{Help: "tab complete", Badges: "12k ctx"}, w)
		}},
		{"footer new output marker", func(p render.Presenter) string {
			return p.Footer(render.ViewState{Help: "tab complete", NewOutput: true}, w)
		}},
		{"footer error line", func(p render.Presenter) string {
			return p.Footer(render.ViewState{Help: "tab complete", Err: "something failed"}, w)
		}},
		{"footer jobs row", func(p render.Presenter) string {
			return p.Footer(render.ViewState{Help: "tab complete", Footers: []render.FooterRow{{Glyph: "*", Text: "2 jobs running", Kind: render.FooterJobs}}}, w)
		}},
		{"footer shell row", func(p render.Presenter) string {
			return p.Footer(render.ViewState{Help: "tab complete", Footers: []render.FooterRow{{Glyph: ">", Text: "1 shell job", Kind: render.FooterShell}}}, w)
		}},
		{"footer loops row", func(p render.Presenter) string {
			return p.Footer(render.ViewState{Help: "tab complete", Footers: []render.FooterRow{{Glyph: "~", Text: "loop every 5m", Kind: render.FooterLoops}}}, w)
		}},
		{"footer goal row", func(p render.Presenter) string {
			return p.Footer(render.ViewState{Help: "tab complete", Footers: []render.FooterRow{{Glyph: "o", Text: "goal: ship it", Kind: render.FooterGoal}}}, w)
		}},
		{"footer unset kind row", func(p render.Presenter) string {
			return p.Footer(render.ViewState{Help: "tab complete", Footers: []render.FooterRow{{Text: "unset kind row"}}}, w)
		}},
		{"footer todo chip", func(p render.Presenter) string {
			return p.Footer(render.ViewState{Help: "tab complete", Footers: []render.FooterRow{{Glyph: "o", Text: "todo 2/5 writing tests", Kind: render.FooterTodo, State: render.ChipActive}}}, w)
		}},
		{"footer focused strip", func(p render.Presenter) string {
			return p.Footer(render.ViewState{Help: "lt/rt move · enter open · esc back", Badges: "12k ctx", Expanded: "session 1k in / 2k out  12:00:00",
				Footers: []render.FooterRow{
					{Glyph: "o", Text: "todo -", Kind: render.FooterTodo},
					{Glyph: ">", Text: "shell 3 done", Kind: render.FooterShell, Alert: "x1", Selected: true},
					{Glyph: ">", Text: "explore 3.1k", Kind: render.FooterJobs, State: render.ChipActive},
				}}, w)
		}},
		{"footer help right hint", func(p render.Presenter) string {
			return p.Footer(render.ViewState{Help: "tab complete", HelpRight: "ctrl+g activity"}, w)
		}},
		{"footer every row", func(p render.Presenter) string { return p.Footer(busyFooter, w) }},
		{"footer height every row", func(p render.Presenter) string { return fmt.Sprintf("%d", p.FooterHeight(busyFooter, w)) }},

		{"frame without dialogs", func(p render.Presenter) string {
			v := render.ViewState{Width: w, Brand: "nib", Cwd: "~/src/project", Help: "tab complete"}
			header, footer := p.Header(v), p.Footer(v, w)
			return p.Frame(v, header, "BODY-MARKER", "COMPOSER-MARKER", footer, w, 40)
		}},
		{"frame with dialogs", func(p render.Presenter) string {
			v := render.ViewState{
				Width: w, Brand: "nib", Cwd: "~/src/project", Help: "tab complete",
				Dialogs: []render.Dialog{{
					Kind: render.DialogAsk, Title: "OVERLAY-MARKER-QUESTION",
					Options: []render.DialogOption{{Text: "OVERLAY-MARKER-OPTION"}},
				}},
			}
			header, footer := p.Header(v), p.Footer(v, w)
			return p.Frame(v, header, "BODY-ONLY-CONTENT", "COMPOSER-MARKER", footer, w, 40)
		}},
	}
}

// renderGolden concatenates every case's output for p, each fenced by its own
// name, into one deterministic block — one file per presenter is enough to
// review as a single diff, and a fenced case name pinpoints exactly which
// method regressed without needing one file per case.
func renderGolden(p render.Presenter) string {
	var b strings.Builder
	for _, c := range goldenCases() {
		out := c.fn(p)
		b.WriteString("=== ")
		b.WriteString(c.name)
		b.WriteString(" ===\n")
		b.WriteString(out)
		if !strings.HasSuffix(out, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// TestGolden is Task 17's safety net (see goldenCases' doc). It must pass,
// UNMODIFIED, both before and after the Base extraction — a failure after the
// extraction means the extraction changed rendered output, and the fix is to
// correct the extraction, never to regenerate this fixture.
func TestGolden(t *testing.T) {
	for name, p := range presenters() {
		t.Run(name, func(t *testing.T) {
			got := renderGolden(p)
			path := filepath.Join("testdata", "golden", name+".golden")

			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading golden fixture %s: %v (run `go test ./tui/render/... -run TestGolden -golden.update` to create it)", path, err)
			}
			if got != string(want) {
				t.Errorf("%s presenter output drifted from %s.\nThis must never happen during Task 17's extraction — fix the extraction, don't regenerate the fixture.\n--- got ---\n%s--- want ---\n%s", name, path, got, string(want))
			}
		})
	}
}
