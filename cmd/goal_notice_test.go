package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

func TestGoalNoticeStreamsAndJSON(t *testing.T) {
	for _, mode := range []string{"cli-pipe", "cli-interactive", "json"} {
		for _, paused := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "-active", true: "-restored-paused"}[paused], func(t *testing.T) {
				srv := goalNoticeLLM(t, "done")
				cfg := approvalConfig(srv.URL)
				cfg.BaseDir = t.TempDir()
				cfg.InitialGoal = "unfinished goal"
				cfg.InitialGoalPaused = paused
				cfg.Goal = types.GoalConfig{MaxReprompts: 1, RepromptWindow: "1h"}
				var out, errs bytes.Buffer
				input := "hello"
				if mode == "cli-interactive" {
					input += "\nexit\n"
				}
				streams := Streams{In: strings.NewReader(input), Out: &out, Err: &errs}
				var err error
				if mode == "json" {
					err = RunJSON(context.Background(), cfg, streams, nil, nil)
				} else {
					err = RunCLI(context.Background(), cfg, streams, nil, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(out.String(), "/goal resume") {
					t.Fatal("unsupported guidance")
				}
				want := 1
				if paused {
					want = 0
				}
				if mode == "json" {
					notices, responses := 0, 0
					for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
						var ev map[string]any
						if err := json.Unmarshal([]byte(line), &ev); err != nil {
							t.Fatal(err)
						}
						switch ev["type"] {
						case "goal_paused":
							notices++
							if ev["kind"] != "goal_paused" || ev["max_reprompts"] != float64(1) || ev["window"] != "1h0m0s" || ev["paused"] != true {
								t.Fatalf("notice: %+v", ev)
							}
						case "response":
							responses++
							if ev["text"] != "done" {
								t.Fatalf("response: %+v", ev)
							}
						}
					}
					if notices != want || responses != 1 {
						t.Fatalf("notices=%d responses=%d output=%s", notices, responses, out.String())
					}
				} else {
					if n := strings.Count(out.String(), "Goal paused after 1 automatic reminders within 1h0m0s"); n != want {
						t.Fatalf("notice count=%d output=%s", n, out.String())
					}
					if !strings.Contains(out.String(), "done") {
						t.Fatal("missing response")
					}
				}
			})
		}
	}
}

func goalNoticeLLM(t *testing.T, answer string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Stream bool `json:"stream"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)

		if !req.Stream {
			resp := map[string]any{
				"id": "1", "object": "chat.completion", "model": "fake",
				"choices": []map[string]any{{
					"index":         0,
					"message":       map[string]any{"role": "assistant", "content": answer},
					"finish_reason": "stop",
				}},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		emit := func(delta map[string]any, finish string) {
			choice := map[string]any{"index": 0, "delta": delta}
			if finish != "" {
				choice["finish_reason"] = finish
			}
			b, _ := json.Marshal(map[string]any{
				"id": "fake", "object": "chat.completion.chunk", "model": "fake",
				"choices": []any{choice},
			})
			_, _ = w.Write([]byte("data: "))
			_, _ = w.Write(b)
			_, _ = w.Write([]byte("\n\n"))
			if fl != nil {
				fl.Flush()
			}
		}
		emit(map[string]any{"content": answer}, "")
		emit(map[string]any{}, "stop")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if fl != nil {
			fl.Flush()
		}
	}))
}
