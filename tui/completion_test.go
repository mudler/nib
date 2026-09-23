package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/slash"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/types"
)

func sampleRegistries() ([]types.CommandConfig, []types.Skill, []types.AgentTypeConfig) {
	return []types.CommandConfig{{Name: "review", Description: "review diff"}},
		[]types.Skill{{Name: "reviewer", Description: "guidelines"}},
		[]types.AgentTypeConfig{{Name: "explore", Description: "read-only"}}
}

func TestBuildAndFilter(t *testing.T) {
	cmds, skills, agents := sampleRegistries()
	items := buildCompItems(cmds, skills, agents)
	if len(items) != 17 {
		t.Fatalf("want 17 items, got %d", len(items))
	}
	got := filterComp(items, "rev")
	if len(got) != 2 {
		t.Fatalf("want 2 matches for 'rev', got %d: %+v", len(got), got)
	}
	if got := filterComp(items, "loop"); len(got) != 1 || got[0].Insert != "/loop " {
		t.Fatalf("filter 'loop' should surface the builtin, got %+v", got)
	}
	if got := filterComp(items, "yolo"); len(got) != 1 || got[0].Insert != "/yolo " {
		t.Fatalf("filter 'yolo' should surface the builtin, got %+v", got)
	}
	if got := filterComp(items, "log"); len(got) != 2 || got[0].Insert != "/login " || got[1].Insert != "/logout " {
		t.Fatalf("filter 'log' should surface /login and /logout, got %+v", got)
	}
	for _, it := range items {
		switch it.Cat {
		case compBuiltin:
			if it.Name == "loop" && it.Insert != "/loop " {
				t.Fatalf("loop insert wrong: %q", it.Insert)
			}
			if it.Name == "compact" && it.Insert != "/compact " {
				t.Fatalf("compact insert wrong: %q", it.Insert)
			}
		case compCmd:
			if it.Insert != "/review " {
				t.Fatalf("cmd insert wrong: %q", it.Insert)
			}
		case compSkill:
			if it.Insert != "/skill reviewer " {
				t.Fatalf("skill insert wrong: %q", it.Insert)
			}
		case compAgent:
			if it.Insert != "/agent explore " {
				t.Fatalf("agent insert wrong: %q", it.Insert)
			}
		}
	}
}

func TestCompStateSyncAndAccept(t *testing.T) {
	cmds, skills, agents := sampleRegistries()
	var c compState
	c.setRegistries(cmds, skills, agents)

	c.sync("/rev")
	if !c.active || len(c.matches) != 2 {
		t.Fatalf("expected active with 2 matches, got active=%v matches=%d", c.active, len(c.matches))
	}
	if g := c.ghost("/rev"); g != "iew " {
		t.Fatalf("ghost wrong: got %q want %q", g, "iew ")
	}
	c.sync("/review the diff")
	if c.active {
		t.Fatal("popup should be inactive once a space is typed")
	}
	c.sync("hello")
	if c.active {
		t.Fatal("popup should be inactive for non-slash input")
	}
	c.sync("/rev")
	got, ok := c.accept()
	if !ok || got != "/review " {
		t.Fatalf("accept wrong: %q ok=%v", got, ok)
	}
}

func TestCompStateNavigation(t *testing.T) {
	cmds, skills, agents := sampleRegistries()
	var c compState
	c.setRegistries(cmds, skills, agents)
	c.sync("/rev")
	c.down()
	if c.sel != 1 {
		t.Fatalf("down: sel=%d", c.sel)
	}
	c.down()
	if c.sel != 1 {
		t.Fatalf("down clamp: sel=%d", c.sel)
	}
	c.up()
	c.up()
	if c.sel != 0 {
		t.Fatalf("up clamp: sel=%d", c.sel)
	}
}

func TestGoalBuiltinInCompletion(t *testing.T) {
	items := buildCompItems(nil, nil, nil)
	for _, it := range items {
		if it.Cat == compBuiltin && it.Name == "goal" {
			if it.Insert != "/goal " {
				t.Fatalf("goal Insert = %q, want %q", it.Insert, "/goal ")
			}
			return
		}
	}
	t.Fatalf("expected /goal built-in in completion items, got %+v", items)
}

// The builtin list is hand-maintained, so a new slash verb is only discoverable
// once someone remembers to add it here. /model and /models were missing until
// they were wired into the front ends.
func TestBuiltinsCoverTheModelVerbs(t *testing.T) {
	cmds, skills, agents := sampleRegistries()
	items := buildCompItems(cmds, skills, agents)

	want := map[string]string{"model": "/model ", "models": "/models "}
	for _, it := range items {
		if it.Cat != compBuiltin {
			continue
		}
		if insert, ok := want[it.Name]; ok {
			if it.Insert != insert {
				t.Fatalf("%s insert = %q, want %q", it.Name, it.Insert, insert)
			}
			delete(want, it.Name)
		}
	}
	for name := range want {
		t.Errorf("builtin completion is missing %q", name)
	}

	// Typing "/model" must offer both, so the list verb stays reachable.
	if got := filterComp(items, "model"); len(got) != 2 {
		t.Fatalf("filter 'model' should surface both verbs, got %+v", got)
	}
}

// The four Enter-vs-completion cases below drive real tea.KeyMsg values
// through Update, the way ask_test.go and scroll_test.go do, and assert the
// DISPATCH happened (session state flipped, a transcript message landed) —
// not merely that the textarea contents changed, since a swallowed Enter
// would still leave the composer looking "handled".

// TestEnterCompletionExactVerbSubmits is the regression case: the composer
// holds exactly "/yolo" and the popup's sole match is "yolo" itself — there
// is nothing left to complete, so Enter must submit instead of accepting
// (which would just insert a trailing space and eat the keypress).
func TestEnterCompletionExactVerbSubmits(t *testing.T) {
	m := newQueueTestModel()
	m.session = &chat.Session{}
	cmds, skills, agents := sampleRegistries()
	m.completion.setRegistries(cmds, skills, agents)

	m.textarea.SetValue("/yolo")
	m.completion.sync(m.textarea.Value())
	if !m.completion.active || len(m.completion.matches) != 1 {
		t.Fatalf("fixture: want popup active with sole match, got active=%v matches=%d", m.completion.active, len(m.completion.matches))
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		cmd()
	}
	nm := next.(Model)

	if !nm.session.AutoApprove() {
		t.Fatal("exact '/yolo' + Enter should submit and toggle auto-approve, not just accept the completion")
	}
	msg := lastMessage(t, nm)
	if msg.Content != theme.YoloOn {
		t.Fatalf("notice = %q, want %q", msg.Content, theme.YoloOn)
	}
}

// TestEnterCompletionPrefixStillCompletes guards the feature this fix must
// not break: a genuine prefix still completes on Enter rather than
// submitting the partial verb.
func TestEnterCompletionPrefixStillCompletes(t *testing.T) {
	m := newQueueTestModel()
	m.session = &chat.Session{}
	cmds, skills, agents := sampleRegistries()
	m.completion.setRegistries(cmds, skills, agents)

	m.textarea.SetValue("/yo")
	m.completion.sync(m.textarea.Value())
	if !m.completion.active || len(m.completion.matches) != 1 {
		t.Fatalf("fixture: want popup active with sole match, got active=%v matches=%d", m.completion.active, len(m.completion.matches))
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("completing a prefix must not start a turn")
	}
	nm := next.(Model)

	if got := nm.textarea.Value(); got != "/yolo " {
		t.Fatalf("textarea = %q, want %q", got, "/yolo ")
	}
	if nm.session.AutoApprove() {
		t.Fatal("prefix completion must not dispatch /yolo")
	}
	if len(nm.messages) != 0 {
		t.Fatalf("prefix completion must not post any message, got %+v", nm.messages)
	}
}

// TestEnterCompletionMultiMatchAcceptsHighlighted covers a popup with several
// live matches: Enter must still accept the highlighted one rather than
// submit the ambiguous partial verb.
func TestEnterCompletionMultiMatchAcceptsHighlighted(t *testing.T) {
	m := newQueueTestModel()
	m.session = &chat.Session{}
	cmds, skills, agents := sampleRegistries()
	m.completion.setRegistries(cmds, skills, agents)

	m.textarea.SetValue("/re")
	m.completion.sync(m.textarea.Value())
	if !m.completion.active || len(m.completion.matches) < 2 {
		t.Fatalf("fixture: want popup active with multiple matches, got active=%v matches=%d", m.completion.active, len(m.completion.matches))
	}
	want := m.completion.matches[m.completion.sel].Insert

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("accepting from a multi-match popup must not start a turn")
	}
	nm := next.(Model)
	if got := nm.textarea.Value(); got != want {
		t.Fatalf("textarea = %q, want %q", got, want)
	}
	if len(nm.messages) != 0 {
		t.Fatalf("accepting from a multi-match popup must not dispatch, got %+v", nm.messages)
	}
}

// TestEnterCompletionModelModelsExactSelectionSubmits and
// TestEnterCompletionModelModelsOtherSelectionCompletes close a real gap in
// the multi-match case: the live built-in list has a genuine prefix pair,
// "/model" and "/models" (tui/completion.go's buildCompItems), so typing the
// shorter verb in full still produces TWO matches from filterComp's
// substring search. Gating exact() on "len(matches) == 1" would miss this
// entirely and reintroduce the double-Enter papercut for "/model" — exact()
// must instead compare against whichever match is currently SELECTED. These
// use the real built-in list (setRegistries(nil, nil, nil)), not the
// review/reviewer fixture pair, so the actual shipped collision is
// exercised.
func TestEnterCompletionModelModelsExactSelectionSubmits(t *testing.T) {
	m := newModelSwitchTestModel(t, "model-a", "model-b")
	m.completion.setRegistries(nil, nil, nil)

	m.textarea.SetValue("/model")
	m.completion.sync(m.textarea.Value())
	if !m.completion.active || len(m.completion.matches) != 2 {
		t.Fatalf("fixture: want the model/models prefix pair, got active=%v matches=%d", m.completion.active, len(m.completion.matches))
	}
	if got := m.completion.matches[m.completion.sel].Name; got != "model" {
		t.Fatalf("fixture: want the selection to start on the exact match 'model', got %q", got)
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("exact '/model' + Enter should start asynchronous picker loading")
	}
	nm := next.(Model)

	if got := nm.textarea.Value(); got != "" {
		t.Fatalf("exact '/model' + Enter should submit and clear the composer, got %q", got)
	}
	msg := lastMessage(t, nm)
	if msg.Role != "user" || msg.Content != "/model" {
		t.Fatalf("exact '/model' + Enter should submit the command, got role %q content %q", msg.Role, msg.Content)
	}
	if !nm.modelPicker.active || !nm.modelPicker.loading {
		t.Fatalf("exact '/model' + Enter should open a loading picker, got %+v", nm.modelPicker)
	}
}

func TestEnterCompletionModelModelsOtherSelectionCompletes(t *testing.T) {
	m := newModelSwitchTestModel(t, "model-a", "model-b")
	m.completion.setRegistries(nil, nil, nil)

	m.textarea.SetValue("/model")
	m.completion.sync(m.textarea.Value())
	m.completion.down() // move the selection off "model" and onto "models"
	if got := m.completion.matches[m.completion.sel].Name; got != "models" {
		t.Fatalf("fixture: want the selection moved to 'models', got %q", got)
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("completing to a different match must not start a turn")
	}
	nm := next.(Model)

	if got := nm.textarea.Value(); got != "/models " {
		t.Fatalf("textarea = %q, want %q", got, "/models ")
	}
	if len(nm.messages) != 0 {
		t.Fatalf("completing to a different match must not dispatch, got %+v", nm.messages)
	}
}

// TestCompBuiltinNamesMatchSlashResolve ties every theme.Comp*Name constant
// to the verb string slash.Resolve actually switches on. These are protocol
// tokens, not copy: CompAttachName and CompResumeName must byte-match
// slash.Resolve's `case "attach"` / `case "resume"` (slash/slash.go) or
// completion silently offers a verb slash.Resolve doesn't recognize, and
// KindError: unknown command %q comes back at Enter with no compile-time
// signal. Before this test, only six of the eight constants (loop, compact,
// goal, model, models, yolo) were incidentally pinned by literal Insert
// assertions elsewhere in this file — attach and resume had no coverage at
// all. Resolving through the real switch (rather than re-typing the case
// strings as literals here) means a future rename of the slash.go case or
// the theme constant breaks this test either way.
func TestCompBuiltinNamesMatchSlashResolve(t *testing.T) {
	names := []string{
		theme.CompLoopName,
		theme.CompCompactName,
		theme.CompGoalName,
		theme.CompModelName,
		theme.CompModelsName,
		theme.CompAttachName,
		theme.CompYoloName,
		theme.CompResumeName,
	}
	for _, name := range names {
		act := slash.Resolve("/"+name, nil, nil, nil)
		if act.Kind == slash.KindError && strings.Contains(act.Err, "unknown command") {
			t.Errorf("theme.Comp*Name %q does not match any slash.Resolve verb: %v", name, act.Err)
		}
	}
}

// TestEnterCompletionFullAgentNameCompletes and
// TestEnterCompletionFullSkillNameCompletes are the regression case for the
// exact() guard introduced this wave: compSkill and compAgent items complete
// to "/skill <name> " / "/agent <name> " (buildCompItems), so their Name
// field alone ("explore", "reviewer") is not the command the way it is for
// compBuiltin/compCmd items. Comparing exact() against "/" + it.Name wrongly
// reported "/explore" (a FULL agent name) as nothing-left-to-complete, so
// Enter fell through to submit — and slash.Resolve has no "explore" verb,
// so it errored instead of completing to "/agent explore ". exact() must key
// off the item's own Insert instead.
func TestEnterCompletionFullAgentNameCompletes(t *testing.T) {
	m := newQueueTestModel()
	m.session = &chat.Session{}
	cmds, skills, agents := sampleRegistries()
	m.completion.setRegistries(cmds, skills, agents)

	m.textarea.SetValue("/explore")
	m.completion.sync(m.textarea.Value())
	if !m.completion.active || len(m.completion.matches) != 1 {
		t.Fatalf("fixture: want popup active with sole match, got active=%v matches=%d", m.completion.active, len(m.completion.matches))
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("completing a full agent name must not start a turn")
	}
	nm := next.(Model)

	if got := nm.textarea.Value(); got != "/agent explore " {
		t.Fatalf("textarea = %q, want %q", got, "/agent explore ")
	}
	if len(nm.messages) != 0 {
		t.Fatalf("completing a full agent name must not dispatch, got %+v", nm.messages)
	}
}

func TestEnterCompletionFullSkillNameCompletes(t *testing.T) {
	m := newQueueTestModel()
	m.session = &chat.Session{}
	cmds, skills, agents := sampleRegistries()
	m.completion.setRegistries(cmds, skills, agents)

	m.textarea.SetValue("/reviewer")
	m.completion.sync(m.textarea.Value())
	if !m.completion.active || len(m.completion.matches) != 1 {
		t.Fatalf("fixture: want popup active with sole match, got active=%v matches=%d", m.completion.active, len(m.completion.matches))
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("completing a full skill name must not start a turn")
	}
	nm := next.(Model)

	if got := nm.textarea.Value(); got != "/skill reviewer " {
		t.Fatalf("textarea = %q, want %q", got, "/skill reviewer ")
	}
	if len(nm.messages) != 0 {
		t.Fatalf("completing a full skill name must not dispatch, got %+v", nm.messages)
	}
}

// TestEnterCompletionEmptyComposerNoOp and
// TestEnterCompletionInactiveSubmitsNormally cover "nothing typed, or no
// popup -> unchanged": the exact-match guard must never engage when there is
// no active completion to suppress.
func TestEnterCompletionEmptyComposerNoOp(t *testing.T) {
	m := newQueueTestModel()
	m.session = &chat.Session{}
	cmds, skills, agents := sampleRegistries()
	m.completion.setRegistries(cmds, skills, agents)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("empty composer should not start a turn")
	}
	nm := next.(Model)
	if len(nm.messages) != 0 {
		t.Fatalf("empty Enter must not post any message, got %+v", nm.messages)
	}
	if nm.completion.active {
		t.Fatal("popup should not activate for an empty composer")
	}
}

func TestEnterCompletionInactiveSubmitsNormally(t *testing.T) {
	m := newQueueTestModel()
	m.session = &chat.Session{}
	cmds, skills, agents := sampleRegistries()
	m.completion.setRegistries(cmds, skills, agents)

	// A trailing argument deactivates the popup (sync() turns off once a
	// space is typed), so this exercises the "no popup" half of the case.
	m.textarea.SetValue("/yolo on")
	m.completion.sync(m.textarea.Value())
	if m.completion.active {
		t.Fatal("fixture: popup should be inactive once a space is typed")
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("/yolo on must not start a turn")
	}
	nm := next.(Model)
	if !nm.session.AutoApprove() {
		t.Fatal("explicit '/yolo on' should submit and turn auto-approve on")
	}
}
