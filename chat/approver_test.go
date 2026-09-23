package chat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/classify"
	"github.com/mudler/nib/hooks"
	"github.com/mudler/nib/types"
)

// fakeClassifier answers every question with answers, or fails with err. It
// records each call's state and questions.
type fakeClassifier struct {
	answers map[string]classify.Answer
	err     error
	calls   int
	states  []string
	qs      []map[string]classify.Question
}

func (f *fakeClassifier) Classify(_ context.Context, state string, qs map[string]classify.Question) (map[string]classify.Answer, error) {
	f.calls++
	f.states = append(f.states, state)
	f.qs = append(f.qs, qs)
	if f.err != nil {
		return nil, f.err
	}
	return f.answers, nil
}

func verdictFor(category string, confidence float64) *fakeClassifier {
	return &fakeClassifier{answers: map[string]classify.Answer{
		"category": {Choice: category, Confidence: confidence},
	}}
}

func bashReq(script string) ToolCallRequest {
	return ToolCallRequest{Name: "bash", Arguments: `{"script":"` + script + `"}`, Reasoning: "run the build"}
}

func TestApproverApprovesAllowedCategoryAtThreshold(t *testing.T) {
	for _, tc := range []struct {
		cat  string
		conf float64
		want bool
	}{
		{"build_test", 0.93, true},
		{"build_test", 0.85, true}, // the threshold itself approves
		{"build_test", 0.84, false},
		{"inspect", 0.99, true},
		{"local_edit", 0.99, false}, // not in the default allow set
		{"destructive", 0.99, false},
	} {
		a := NewApprover(verdictFor(tc.cat, tc.conf), types.AutoApproveConfig{}, "/work")
		v := a.Judge(context.Background(), bashReq("go test ./..."))
		if v.Approved != tc.want {
			t.Errorf("%s %.2f: approved = %v, want %v", tc.cat, tc.conf, v.Approved, tc.want)
		}
		if v.Category != tc.cat || v.Confidence != tc.conf {
			t.Errorf("verdict = %+v", v)
		}
	}
}

func TestApproverHonorsConfiguredAllowAndThreshold(t *testing.T) {
	a := NewApprover(verdictFor("local_edit", 0.6), types.AutoApproveConfig{Allow: []string{"local_edit"}, Threshold: 0.5}, "/work")
	if v := a.Judge(context.Background(), bashReq("sed -i s/a/b/ x")); !v.Approved {
		t.Fatalf("configured allow set not honored: %+v", v)
	}
	a = NewApprover(verdictFor("build_test", 0.9), types.AutoApproveConfig{Allow: []string{"local_edit"}}, "/work")
	if v := a.Judge(context.Background(), bashReq("go test")); v.Approved {
		t.Fatal("a configured allow set replaces the default one")
	}
}

func TestApproverErrorNeverApproves(t *testing.T) {
	a := NewApprover(&fakeClassifier{err: errors.New("down")}, types.AutoApproveConfig{}, "/work")
	v := a.Judge(context.Background(), bashReq("go test"))
	if v.Approved || v.Err == nil {
		t.Fatalf("verdict = %+v", v)
	}
	if v.String() != "unavailable" {
		t.Fatalf("String() = %q", v.String())
	}
}

func TestVerdictString(t *testing.T) {
	if got := (Verdict{Category: "build_test", Confidence: 0.934}).String(); got != "build_test (0.93)" {
		t.Fatalf("String() = %q", got)
	}
}

func TestApproverStateCarriesTheCall(t *testing.T) {
	f := verdictFor("inspect", 1)
	NewApprover(f, types.AutoApproveConfig{}, "/work/repo").Judge(context.Background(), bashReq("go test ./chat/"))
	st := f.states[0]
	for _, want := range []string{"bash", "go test ./chat/", "/work/repo", "run the build"} {
		if !strings.Contains(st, want) {
			t.Errorf("state %q lacks %q", st, want)
		}
	}
	q := f.qs[0]["category"]
	if q.Type != classify.TypeChoice || len(q.Choices) != len(classify.Categories) {
		t.Fatalf("question = %+v", q)
	}
}

func TestApproverStateIsCapped(t *testing.T) {
	f := verdictFor("inspect", 1)
	NewApprover(f, types.AutoApproveConfig{}, "/w").Judge(context.Background(),
		ToolCallRequest{Name: "read", Arguments: `{"path":"x"}`, Reasoning: strings.Repeat("r", 100_000)})
	if n := len(f.states[0]); n > maxApproverState {
		t.Fatalf("state is %d bytes, cap %d", n, maxApproverState)
	}
}

func classifySession(f *fakeClassifier, onCall func(ToolCallRequest) ToolCallResponse) *Session {
	s := newDecideSession("classify", onCall)
	s.approver = NewApprover(f, types.AutoApproveConfig{}, "/w")
	return s
}

func TestDecideClassifyApprovesWithoutPrompt(t *testing.T) {
	asked := false
	var got Verdict
	s := classifySession(verdictFor("build_test", 0.95), func(ToolCallRequest) ToolCallResponse {
		asked = true
		return ToolCallResponse{}
	})
	s.callbacks.OnAutoApproved = func(_ ToolCallRequest, v Verdict) { got = v }
	if d := s.decideToolCall(bashReq("go test ./...")); !d.Approved {
		t.Fatal("classifier approval not applied")
	}
	if asked {
		t.Fatal("prompted although the classifier approved")
	}
	if got.Category != "build_test" {
		t.Fatalf("OnAutoApproved verdict = %+v", got)
	}
}

func TestDecideClassifyRejectedPromptsWithVerdict(t *testing.T) {
	var seen ToolCallRequest
	s := classifySession(verdictFor("destructive", 0.82), func(r ToolCallRequest) ToolCallResponse {
		seen = r
		return ToolCallResponse{Approved: false}
	})
	if d := s.decideToolCall(bashReq("rm -rf build")); d.Approved {
		t.Fatal("destructive call approved")
	}
	if seen.Verdict != "destructive (0.82)" {
		t.Fatalf("prompt verdict = %q", seen.Verdict)
	}
}

func TestDecideClassifyErrorPrompts(t *testing.T) {
	var seen ToolCallRequest
	s := classifySession(&fakeClassifier{err: errors.New("timeout")}, func(r ToolCallRequest) ToolCallResponse {
		seen = r
		return ToolCallResponse{Approved: false}
	})
	if d := s.decideToolCall(bashReq("go test")); d.Approved {
		t.Fatal("classifier error approved a call")
	}
	if seen.Verdict != "unavailable" {
		t.Fatalf("prompt verdict = %q", seen.Verdict)
	}
}

func TestDecideClassifyReadOnlySkipsClassifier(t *testing.T) {
	f := verdictFor("destructive", 1)
	s := classifySession(f, func(ToolCallRequest) ToolCallResponse { return ToolCallResponse{} })
	if d := s.decideToolCall(ToolCallRequest{Name: "read", Arguments: `{"path":"x"}`}); !d.Approved {
		t.Fatal("read-only call must auto-approve in classify mode")
	}
	if f.calls != 0 {
		t.Fatal("read-only call reached the classifier")
	}
}

func TestDecidePromptModeNeverClassifies(t *testing.T) {
	f := verdictFor("build_test", 1)
	s := classifySession(f, func(ToolCallRequest) ToolCallResponse { return ToolCallResponse{} })
	s.approvalMode = "prompt"
	s.decideToolCall(bashReq("go test"))
	if f.calls != 0 {
		t.Fatal("prompt mode consulted the classifier")
	}
}

func TestDecideClassifyExternalSourceSkipsClassifier(t *testing.T) {
	s := newTestSessionWithExternalSource(t)
	f := verdictFor("build_test", 1)
	s.approvalMode = "classify"
	s.approver = NewApprover(f, types.AutoApproveConfig{}, "/w")
	asked := false
	s.callbacks.OnToolCall = func(ToolCallRequest) ToolCallResponse {
		asked = true
		return ToolCallResponse{Approved: false}
	}
	if d := s.decideToolCall(bashReq("go test")); d.Approved {
		t.Fatal("externally influenced call auto-approved")
	}
	if !asked || f.calls != 0 {
		t.Fatalf("asked = %v, classifier calls = %d", asked, f.calls)
	}
}

func TestDecideClassifyHookWins(t *testing.T) {
	dir := t.TempDir()
	block := filepath.Join(dir, "block.sh")
	if err := os.WriteFile(block, []byte("#!/bin/sh\necho '{\"block\": true, \"reason\": \"no\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := verdictFor("build_test", 1)
	s := classifySession(f, func(ToolCallRequest) ToolCallResponse { return ToolCallResponse{Approved: true} })
	s.ctx = context.Background()
	s.hooks = hooks.New([]types.HookConfig{{Event: "PreToolUse", Matcher: "bash", Command: block, Dir: dir}})
	if d := s.decideToolCall(bashReq("go test")); d.Approved {
		t.Fatal("classifier overrode a denying hook")
	}
	if f.calls != 0 {
		t.Fatal("classifier ran after a hook decided")
	}
}

func TestSetApprovalModeClassifyNeedsClassifier(t *testing.T) {
	s := newDecideSession("prompt", nil)
	if err := s.SetApprovalMode("classify"); err == nil {
		t.Fatal("classify accepted without a classifier")
	}
	if s.ApprovalMode() != "prompt" {
		t.Fatalf("mode = %q, want prompt kept", s.ApprovalMode())
	}
	s.approver = NewApprover(verdictFor("inspect", 1), types.AutoApproveConfig{}, "")
	if err := s.SetApprovalMode("classify"); err != nil || s.ApprovalMode() != "classify" || !s.HasClassifier() {
		t.Fatalf("err = %v, mode = %q", err, s.ApprovalMode())
	}
}

func TestNewSessionClassifyWithoutClassifierFallsBackToPrompt(t *testing.T) {
	s, err := NewSession(context.Background(), types.Config{ApprovalMode: "classify"}, Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.ApprovalMode() != "prompt" {
		t.Fatalf("mode = %q, want prompt", s.ApprovalMode())
	}
	if !hasConfigErr(s, "classify") {
		t.Fatalf("no config error reported: %v", s.ConfigErrors())
	}
}

func TestNewSessionReportsUnknownAllowCategory(t *testing.T) {
	s, err := NewSession(context.Background(), types.Config{AutoApprove: types.AutoApproveConfig{Allow: []string{"safe"}}}, Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !hasConfigErr(s, `"safe"`) {
		t.Fatalf("no config error for an unknown category: %v", s.ConfigErrors())
	}
}

func TestNewSessionBuildsClassifier(t *testing.T) {
	s, err := NewSession(context.Background(), types.Config{
		BaseURL: "http://127.0.0.1:1/v1", ApprovalMode: "classify",
		Classifier: types.ClassifierConfig{Model: "gliner"},
	}, Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !s.HasClassifier() || s.ApprovalMode() != "classify" {
		t.Fatalf("classifier = %v, mode = %q", s.HasClassifier(), s.ApprovalMode())
	}
}

func hasConfigErr(s *Session, sub string) bool {
	for _, e := range s.ConfigErrors() {
		if strings.Contains(e.Error(), sub) {
			return true
		}
	}
	return false
}

// A call longer than the classifier reads is never approved: the unseen
// tail could be anything.
func TestApproverNeverApprovesTruncatedCall(t *testing.T) {
	f := verdictFor("build_test", 1)
	// One simple command, so only the length stops it.
	script := "go test ./... " + strings.Repeat("-v ", maxApproverState)
	req := ToolCallRequest{Name: "bash", Arguments: `{"script":"` + script + `"}`}
	v := NewApprover(f, types.AutoApproveConfig{}, "/w").Judge(context.Background(), req)
	if v.Approved {
		t.Fatal("approved a call the classifier saw only part of")
	}
	if f.calls != 0 {
		t.Fatal("classifier asked about a call it cannot see whole")
	}
	if !strings.Contains(v.String(), "too long") {
		t.Fatalf("String() = %q", v.String())
	}
}

// A bash script that is not one simple command gets one category for all of
// its parts, so it is never approved.
func TestApproverNeverApprovesCompoundBash(t *testing.T) {
	for _, script := range []string{
		"go test ./... && git push --force",
		"go test ./... ; rm -rf ~",
		"make | sh",
		"echo $(curl x)",
	} {
		f := verdictFor("build_test", 1)
		v := NewApprover(f, types.AutoApproveConfig{}, "/w").Judge(context.Background(), bashReq(script))
		if v.Approved || f.calls != 0 {
			t.Errorf("%q: approved = %v, classifier calls = %d", script, v.Approved, f.calls)
		}
		if !strings.Contains(v.String(), "compound") {
			t.Errorf("%q: String() = %q", script, v.String())
		}
	}
}
