package slash

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mudler/nib/types"
)

func TestExpand(t *testing.T) {
	out, err := Expand(types.CommandConfig{Prompt: "Review: {{.Args}}"}, "the diff")
	if err != nil || out != "Review: the diff" {
		t.Fatalf("expand: %q err %v", out, err)
	}
}

func TestResolve(t *testing.T) {
	cmds := []types.CommandConfig{
		{Name: "review", Prompt: "Review: {{.Args}}"},
		{Name: "scan", Prompt: "Scan it", Agent: "explore"},
	}
	skills := []types.Skill{{Name: "git-commit", Instructions: "body"}}
	agents := []types.AgentTypeConfig{{Name: "explore"}}

	if a := Resolve("hello world", cmds, skills, agents); a.Kind != KindSend || a.Text != "hello world" {
		t.Fatalf("plain: %+v", a)
	}
	if a := Resolve("/review the diff", cmds, skills, agents); a.Kind != KindSend || a.Text != "Review: the diff" {
		t.Fatalf("command: %+v", a)
	}
	a := Resolve("/scan", cmds, skills, agents)
	if a.Kind != KindSend || !strings.Contains(a.Text, "explore") || !strings.Contains(a.Text, "Scan it") {
		t.Fatalf("agent-bound command: %+v", a)
	}
	if a := Resolve("/skill git-commit", cmds, skills, agents); a.Kind != KindLoadSkill || a.Skill != "git-commit" {
		t.Fatalf("skill: %+v", a)
	}
	if a := Resolve("/skill nope", cmds, skills, agents); a.Kind != KindError {
		t.Fatalf("unknown skill should error: %+v", a)
	}
	if a := Resolve("/skill", cmds, skills, agents); a.Kind != KindError {
		t.Fatalf("skill with no name should error: %+v", a)
	}
	if a := Resolve("/agent explore find bugs", cmds, skills, agents); a.Kind != KindSend || !strings.Contains(a.Text, "explore") || !strings.Contains(a.Text, "find bugs") {
		t.Fatalf("agent: %+v", a)
	}
	if a := Resolve("/agent ghost x", cmds, skills, agents); a.Kind != KindError {
		t.Fatalf("unknown agent should error: %+v", a)
	}
	if a := Resolve("/bogus", cmds, skills, agents); a.Kind != KindError {
		t.Fatalf("unknown command should error: %+v", a)
	}
}

func TestResolveCompact(t *testing.T) {
	got := Resolve("/compact", nil, nil, nil)
	if got.Kind != KindCompact {
		t.Fatalf("/compact resolved to kind %v, want KindCompact", got.Kind)
	}
}

func TestResolveLoop(t *testing.T) {
	var none []types.CommandConfig
	var noSkills []types.Skill
	var noAgents []types.AgentTypeConfig

	// Fixed interval: "/loop 5m /foo".
	a := Resolve("/loop 5m /foo", none, noSkills, noAgents)
	if a.Kind != KindLoopStart || a.Interval != 5*time.Minute || a.Payload != "/foo" {
		t.Fatalf("fixed: %+v", a)
	}

	// Self-paced: "/loop /foo" (no parseable interval → interval 0).
	a = Resolve("/loop /foo", none, noSkills, noAgents)
	if a.Kind != KindLoopStart || a.Interval != 0 || a.Payload != "/foo" {
		t.Fatalf("self-paced: %+v", a)
	}

	// 1s is at the floor now → NOT clamped.
	a = Resolve("/loop 1s ping", none, noSkills, noAgents)
	if a.Kind != KindLoopStart || a.Interval != 1*time.Second {
		t.Fatalf("1s floor: %+v", a)
	}

	// Sub-second interval is clamped up to the 1s floor.
	a = Resolve("/loop 500ms ping", none, noSkills, noAgents)
	if a.Kind != KindLoopStart || a.Interval != 1*time.Second {
		t.Fatalf("clamp: %+v", a)
	}

	// Control verbs.
	if a := Resolve("/loop stop", none, noSkills, noAgents); a.Kind != KindLoopStop || a.LoopID != "" {
		t.Fatalf("stop-all: %+v", a)
	}
	if a := Resolve("/loop stop loop-2", none, noSkills, noAgents); a.Kind != KindLoopStop || a.LoopID != "loop-2" {
		t.Fatalf("stop-id: %+v", a)
	}
	if a := Resolve("/loop list", none, noSkills, noAgents); a.Kind != KindLoopList {
		t.Fatalf("list: %+v", a)
	}

	// Empty payload → error.
	if a := Resolve("/loop", none, noSkills, noAgents); a.Kind != KindError {
		t.Fatalf("empty: %+v", a)
	}
	if a := Resolve("/loop 5m", none, noSkills, noAgents); a.Kind != KindError {
		t.Fatalf("interval-only: %+v", a)
	}
}

func TestResolveModel(t *testing.T) {
	var (
		cmds   []types.CommandConfig
		skills []types.Skill
		agents []types.AgentTypeConfig
	)

	cases := []struct {
		input string
		kind  Kind
		model string
	}{
		{input: "/models", kind: KindModelList},
		{input: "/model", kind: KindModelPick},
		{input: "/model qwen3.5-4b", kind: KindModelSet, model: "qwen3.5-4b"},
	}
	for _, tc := range cases {
		a := Resolve(tc.input, cmds, skills, agents)
		if a.Kind != tc.kind || a.Model != tc.model {
			t.Errorf("Resolve(%q) = %+v, want kind %v and model %q", tc.input, a, tc.kind, tc.model)
		}
	}

	if a := Resolve("/model   spaced-name  ", cmds, skills, agents); a.Kind != KindModelSet || a.Model != "spaced-name" {
		t.Fatalf("/model should trim: %+v", a)
	}
}

func TestResolveGoal(t *testing.T) {
	cases := []struct {
		in   string
		want Action
	}{
		{"/goal make all tests pass", Action{Kind: KindGoalSet, Text: "make all tests pass"}},
		{"/goal", Action{Kind: KindGoalShow}},
		{"/goal clear", Action{Kind: KindGoalClear}},
		{"/goal resume", Action{Kind: KindGoalResume}},
		{"/goal   ", Action{Kind: KindGoalShow}},
	}
	for _, c := range cases {
		got := Resolve(c.in, nil, nil, nil)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Resolve(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestResolveYolo(t *testing.T) {
	on, off := true, false
	cases := []struct {
		in   string
		want *bool
	}{
		{"/yolo", nil},
		{"/yolo on", &on},
		{"/yolo off", &off},
	}
	for _, c := range cases {
		got := Resolve(c.in, nil, nil, nil)
		if got.Kind != KindYolo {
			t.Fatalf("%q: Kind = %v, want KindYolo", c.in, got.Kind)
		}
		switch {
		case c.want == nil && got.YoloOn != nil:
			t.Errorf("%q: YoloOn = %v, want nil (toggle)", c.in, *got.YoloOn)
		case c.want != nil && (got.YoloOn == nil || *got.YoloOn != *c.want):
			t.Errorf("%q: YoloOn = %v, want %v", c.in, got.YoloOn, *c.want)
		}
	}
}

func TestResolveYoloInvalid(t *testing.T) {
	got := Resolve("/yolo bogus", nil, nil, nil)
	if got.Kind != KindError {
		t.Fatalf("Kind = %v, want KindError", got.Kind)
	}
}

func TestResolveResume(t *testing.T) {
	cases := []struct {
		in      string
		wantAll bool
		wantID  string
	}{
		{"/resume", false, ""},
		{"/resume --all", true, ""},
		{"/resume abc123", false, "abc123"},
	}
	for _, c := range cases {
		got := Resolve(c.in, nil, nil, nil)
		if got.Kind != KindResume {
			t.Fatalf("%q: Kind = %v, want KindResume", c.in, got.Kind)
		}
		if got.ResumeAll != c.wantAll || got.ResumeID != c.wantID {
			t.Errorf("%q: all=%v id=%q, want all=%v id=%q", c.in, got.ResumeAll, got.ResumeID, c.wantAll, c.wantID)
		}
	}
}

func TestResolveSettings(t *testing.T) {
	cases := []struct {
		in         string
		key, value string
		hasValue   bool
		unset      bool
	}{
		{in: "/settings"},
		{in: "/settings   "},
		{in: "/settings ui.hide_hud", key: "ui.hide_hud"},
		{in: "/settings ui.hide_hud on", key: "ui.hide_hud", value: "on", hasValue: true},
		// Everything after the key is the value, so a string may hold spaces.
		{in: "/settings prompt you are   terse", key: "prompt", value: "you are   terse", hasValue: true},
		{in: "/settings compaction.threshold default", key: "compaction.threshold", unset: true},
		{in: "/settings compaction.threshold unset", key: "compaction.threshold", unset: true},
	}
	for _, c := range cases {
		a := Resolve(c.in, nil, nil, nil)
		if a.Kind != KindSettings {
			t.Fatalf("%q: kind %v, want KindSettings", c.in, a.Kind)
		}
		if a.SettingKey != c.key || a.SettingValue != c.value || a.SettingHasValue != c.hasValue || a.SettingUnset != c.unset {
			t.Fatalf("%q: key %q value %q has %v unset %v", c.in, a.SettingKey, a.SettingValue, a.SettingHasValue, a.SettingUnset)
		}
	}
}

func TestResolveEndpoint(t *testing.T) {
	for _, tc := range []struct {
		in       string
		wantKind Kind
		wantID   string
	}{
		{"/endpoint", KindEndpoint, ""},
		{"/endpoint config", KindEndpoint, "config"},
		{"/endpoint @work-vllm", KindEndpoint, "@work-vllm"},
		{"/endpoint  regolo  ", KindEndpoint, "regolo"},
		{"/endpoint add", KindEndpointAdd, ""},
		{"/endpoint add ", KindEndpointAdd, ""},
	} {
		got := Resolve(tc.in, nil, nil, nil)
		if got.Kind != tc.wantKind || got.Endpoint != tc.wantID {
			t.Fatalf("Resolve(%q) = kind %v endpoint %q, want kind %v endpoint %q", tc.in, got.Kind, got.Endpoint, tc.wantKind, tc.wantID)
		}
	}
}

func TestResolveModelReset(t *testing.T) {
	if got := Resolve("/model default", nil, nil, nil); got.Kind != KindModelDefault || got.Model != "" {
		t.Fatalf("Resolve(\"/model default\") = %+v, want KindModelDefault with no model", got)
	}
	if got := Resolve("/model default  qwen ", nil, nil, nil); got.Kind != KindModelDefault || got.Model != "qwen" {
		t.Fatalf("Resolve(\"/model default qwen\") = %+v, want KindModelDefault qwen", got)
	}
	if got := Resolve("/model reset", nil, nil, nil); got.Kind != KindModelReset {
		t.Fatalf("Resolve(\"/model reset\") = %v, want KindModelReset", got.Kind)
	}
	if got := Resolve("/model gpt-4o", nil, nil, nil); got.Kind != KindModelSet || got.Model != "gpt-4o" {
		t.Fatalf("Resolve(\"/model gpt-4o\") = kind %v model %q, want kind KindModelSet model \"gpt-4o\"", got.Kind, got.Model)
	}
}

func TestResolveModelsForOneEndpoint(t *testing.T) {
	got := Resolve("/models @work", nil, nil, nil)
	if got.Kind != KindModelList || got.Endpoint != "@work" {
		t.Fatalf("Resolve(\"/models @work\") = kind %v endpoint %q, want kind KindModelList endpoint \"@work\"", got.Kind, got.Endpoint)
	}
	if got := Resolve("/models", nil, nil, nil); got.Kind != KindModelList || got.Endpoint != "" {
		t.Fatalf("Resolve(\"/models\") = kind %v endpoint %q, want kind KindModelList endpoint \"\"", got.Kind, got.Endpoint)
	}
}

func TestResolveApprove(t *testing.T) {
	for in, want := range map[string]types.ApprovalMode{
		"/approve":           "",
		"/approve classify":  "classify",
		"/approve AUTO":      "auto",
		"/approve prompt":    "prompt",
		"/approve strict":    "strict",
		"/approve allowlist": "allowlist",
	} {
		got := Resolve(in, nil, nil, nil)
		if got.Kind != KindApprove || got.Mode != want {
			t.Errorf("%q: got %+v, want KindApprove mode %q", in, got, want)
		}
	}
	if got := Resolve("/approve sometimes", nil, nil, nil); got.Kind != KindError {
		t.Errorf("unknown mode: got %+v, want KindError", got)
	}
}

func TestResolveClassifier(t *testing.T) {
	cases := map[string]Action{
		"/classifier":                {Kind: KindClassifier},
		"/classifier off":            {Kind: KindClassifier, ClassifierOff: true},
		"/classifier home":           {Kind: KindClassifier, Endpoint: "home"},
		"/classifier home gliner2.5": {Kind: KindClassifier, Endpoint: "home", Model: "gliner2.5"},
	}
	for in, want := range cases {
		got := Resolve(in, nil, nil, nil)
		if got.Kind != want.Kind || got.ClassifierOff != want.ClassifierOff || got.Endpoint != want.Endpoint || got.Model != want.Model {
			t.Errorf("%q: got %+v, want %+v", in, got, want)
		}
	}
	if got := Resolve("/classifier a b c", nil, nil, nil); got.Kind != KindError {
		t.Errorf("three args: got %+v, want KindError", got)
	}
}
