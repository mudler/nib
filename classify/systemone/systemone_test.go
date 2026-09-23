package systemone_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mudler/nib/classify"
	"github.com/mudler/nib/classify/systemone"
)

// server answers every request with body and records the last request.
func server(t *testing.T, status int, body string, got *map[string]any, auth *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" || r.Method != http.MethodPost {
			http.Error(w, "wrong route "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		if auth != nil {
			*auth = r.Header.Get("Authorization")
		}
		if got != nil {
			_ = json.NewDecoder(r.Body).Decode(got)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const choiceAnswer = `{"model":"gliner","answers":{"category":{"type":"choice","choice":"build_test","confidence":0.93,"probabilities":{"build_test":0.93,"inspect":0.07}}}}`

func TestClassifyEncodesRequest(t *testing.T) {
	var got map[string]any
	srv := server(t, 200, `{"answers":{"c":{"type":"choice","choice":"a","confidence":1},"s":{"type":"score","score":1,"confidence":0.5},"n":{"type":"noul","noul":0.2}}}`, &got, nil)
	c := systemone.New(srv.URL+"/v1", "", "gliner2.5", time.Second)
	_, err := c.Classify(context.Background(), "go test ./...", map[string]classify.Question{
		"c": {Type: "choice", Instructions: "pick", Choices: map[string]string{"a": "first"}},
		"s": {Type: "score", Instructions: "rate", Levels: []string{"low", "high"}},
		"n": {Type: "noul", Instructions: "an option"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["state"] != "go test ./..." || got["model"] != "gliner2.5" {
		t.Fatalf("state/model = %v / %v", got["state"], got["model"])
	}
	qs := got["questions"].(map[string]any)
	if crit := qs["c"].(map[string]any)["criteria"]; crit.(map[string]any)["a"] != "first" {
		t.Fatalf("choice criteria = %#v, want object", crit)
	}
	if crit := qs["s"].(map[string]any)["criteria"].([]any); len(crit) != 2 || crit[1] != "high" {
		t.Fatalf("score criteria = %#v, want array", crit)
	}
	if _, ok := qs["n"].(map[string]any)["criteria"]; ok {
		t.Fatal("noul question must not carry criteria")
	}
	if qs["c"].(map[string]any)["instructions"] != "pick" {
		t.Fatalf("instructions missing: %#v", qs["c"])
	}
}

func TestClassifyDecodesAnswers(t *testing.T) {
	srv := server(t, 200, `{"answers":{"category":{"type":"choice","choice":"build_test","confidence":0.93,"probabilities":{"build_test":0.93}},"offers":{"type":"noul","noul":0.8,"entities":[{"text":"Postgres","start":4,"end":12,"confidence":0.9}]}}}`, nil, nil)
	c := systemone.New(srv.URL+"/v1", "", "", time.Second)
	ans, err := c.Classify(context.Background(), "x", map[string]classify.Question{
		"category": {Type: "choice", Choices: map[string]string{"build_test": ""}},
		"offers":   {Type: "noul"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if a := ans["category"]; a.Choice != "build_test" || a.Confidence != 0.93 || a.Probabilities["build_test"] != 0.93 {
		t.Fatalf("choice answer = %+v", a)
	}
	if a := ans["offers"]; a.Noul != 0.8 || len(a.Entities) != 1 || a.Entities[0].Text != "Postgres" || a.Entities[0].Confidence != 0.9 || a.Entities[0].End != 12 {
		t.Fatalf("noul answer = %+v", a)
	}
}

func TestClassifySendsBearerOnlyWithKey(t *testing.T) {
	var auth string
	srv := server(t, 200, choiceAnswer, nil, &auth)
	q := map[string]classify.Question{"category": {Type: "choice", Choices: map[string]string{"build_test": ""}}}
	if _, err := systemone.New(srv.URL+"/v1", "k", "", time.Second).Classify(context.Background(), "x", q); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer k" {
		t.Fatalf("auth = %q", auth)
	}
	if _, err := systemone.New(srv.URL+"/v1", "", "", time.Second).Classify(context.Background(), "x", q); err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		t.Fatalf("auth without key = %q", auth)
	}
}

func TestClassifyErrors(t *testing.T) {
	q := map[string]classify.Question{"category": {Type: "choice", Choices: map[string]string{"a": ""}}}
	cases := map[string]struct {
		status int
		body   string
		want   string
	}{
		"http error":     {500, "boom", "500"},
		"missing answer": {200, `{"answers":{}}`, "category"},
		"wrong type":     {200, `{"answers":{"category":{"type":"noul","noul":0.4}}}`, "category"},
		"no choice":      {200, `{"answers":{"category":{"type":"choice"}}}`, "category"},
		"bad json":       {200, `{`, "decode"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := server(t, tc.status, tc.body, nil, nil)
			_, err := systemone.New(srv.URL+"/v1", "", "", time.Second).Classify(context.Background(), "x", q)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestClassifyTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(300 * time.Millisecond):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	start := time.Now()
	_, err := systemone.New(srv.URL+"/v1", "", "", 50*time.Millisecond).Classify(context.Background(), "x",
		map[string]classify.Question{"c": {Type: "noul"}})
	if err == nil {
		t.Fatal("want a timeout error")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("timeout not applied: took %v", time.Since(start))
	}
}
