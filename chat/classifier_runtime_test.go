package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

func runtimeCfg(endpoint, model string) types.Config {
	return types.Config{
		BaseURL:    "http://127.0.0.1:1/v1",
		Endpoints:  types.Endpoints{{Name: "home", ModelProviderConfig: types.ModelProviderConfig{BaseURL: "http://127.0.0.1:2/v1"}}},
		Classifier: types.ClassifierConfig{Endpoint: endpoint, Model: model},
	}
}

func TestSetClassifierInstallsOne(t *testing.T) {
	s := newDecideSession("prompt", nil)
	if _, err := s.SetClassifier(runtimeCfg("home", "gliner")); err != nil {
		t.Fatal(err)
	}
	if !s.HasClassifier() || !s.SuggestionsEnabled() {
		t.Fatal("classifier not installed")
	}
	if info := s.ClassifierInfo(); !strings.Contains(info, "gliner") || !strings.Contains(info, "home") {
		t.Fatalf("info = %q", info)
	}
	if err := s.SetApprovalMode("classify"); err != nil {
		t.Fatalf("classify refused after SetClassifier: %v", err)
	}
}

func TestSetClassifierOffLeavesClassifyMode(t *testing.T) {
	s := newDecideSession("prompt", nil)
	if _, err := s.SetClassifier(runtimeCfg("", "gliner")); err != nil {
		t.Fatal(err)
	}
	_ = s.SetApprovalMode("classify")
	fellBack, err := s.SetClassifier(types.Config{})
	if err != nil || !fellBack {
		t.Fatalf("fellBack = %v, err = %v", fellBack, err)
	}
	if s.HasClassifier() || s.SuggestionsEnabled() || s.ApprovalMode() != "prompt" || s.ClassifierInfo() != "" {
		t.Fatalf("classifier %v, suggestions %v, mode %q, info %q", s.HasClassifier(), s.SuggestionsEnabled(), s.ApprovalMode(), s.ClassifierInfo())
	}
}

func TestSetClassifierErrorKeepsPrevious(t *testing.T) {
	s := newDecideSession("prompt", nil)
	if _, err := s.SetClassifier(runtimeCfg("", "first")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetClassifier(runtimeCfg("nope", "second")); err == nil {
		t.Fatal("unknown endpoint accepted")
	}
	bad := runtimeCfg("", "third")
	bad.AutoApprove.Allow = []string{"safe"}
	if _, err := s.SetClassifier(bad); err == nil {
		t.Fatal("unknown category accepted")
	}
	if info := s.ClassifierInfo(); !strings.Contains(info, "first") {
		t.Fatalf("previous classifier lost: %q", info)
	}
}

func TestSetClassifierAppliesToTheGate(t *testing.T) {
	asked := false
	s := newDecideSession("prompt", func(ToolCallRequest) ToolCallResponse {
		asked = true
		return ToolCallResponse{}
	})
	s.ctx = context.Background()
	f := verdictFor("build_test", 1)
	s.setClassifierState(&classifierState{approver: NewApprover(f, types.AutoApproveConfig{}, "")})
	_ = s.SetApprovalMode("classify")
	if d := s.decideToolCall(bashReq("go test")); !d.Approved || asked {
		t.Fatal("installed classifier not used by the gate")
	}
	_, _ = s.SetClassifier(types.Config{})
	s.decideToolCall(bashReq("go test"))
	if !asked || f.calls != 1 {
		t.Fatalf("after removal: asked = %v, classifier calls = %d", asked, f.calls)
	}
}

func TestClassifierChoice(t *testing.T) {
	base := types.ClassifierConfig{API: "systemone", Timeout: 3}
	for _, tc := range []struct {
		id, model, wantEndpoint string
		wantErr                 bool
	}{
		{"@home", "g", "home", false},
		{"home", "g", "home", false},
		{"config", "g", "", false},
		{"config.yaml", "g", "", false},
		{"config", "", "", true},
		{"home", "", "home", false},
	} {
		c, err := ClassifierChoice(base, tc.id, tc.model)
		if (err != nil) != tc.wantErr || (err == nil && (c.Endpoint != tc.wantEndpoint || c.Model != tc.model || c.API != "systemone" || c.Timeout != 3)) {
			t.Errorf("%q %q: got %+v, %v", tc.id, tc.model, c, err)
		}
	}
}
