package chat

import (
	"context"
	"fmt"
	"github.com/mudler/nib/types"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelChoicesPreservesPartialDiagnostic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/models" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.Error(w, "no inference", 500)
			return
		}
		if r.URL.Query().Get("after_id") != "" {
			http.Error(w, "later failure", 500)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"claude-future-exact"}],"has_more":true,"last_id":"next"}`)
	}))
	defer srv.Close()
	s := newProviderSession(t, types.ModelProviderConfig{Provider: "anthropic", Model: "current", APIKey: "test", BaseURL: srv.URL})
	ids, partial, err := s.ModelChoices(context.Background(), "")
	if len(ids) != 1 || ids[0] != "claude-future-exact" || !partial || err == nil {
		t.Fatalf("ids=%v partial=%v err=%v", ids, partial, err)
	}
	notice, err := s.SwitchModel(context.Background(), "claude-other-exact")
	if err != nil || s.Model() != "claude-other-exact" || !strings.Contains(notice, "not in the suggested list; the provider decides") {
		t.Fatalf("notice=%q err=%v model=%s", notice, err, s.Model())
	}
}
