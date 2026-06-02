package chat

import "testing"

func TestAskUserToolRun(t *testing.T) {
	var got AskRequest
	tool := &askUserTool{ask: func(req AskRequest) string {
		got = req
		return "the answer"
	}}

	out, _, err := tool.Run(map[string]any{
		"question": "Which one?",
		"options":  []any{"a", "b"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "the answer" {
		t.Fatalf("answer = %q", out)
	}
	if got.Question != "Which one?" || len(got.Options) != 2 || got.Options[0] != "a" {
		t.Fatalf("request parsed wrong: %+v", got)
	}

	safe := &askUserTool{}
	if out, _, err := safe.Run(map[string]any{"question": "x"}); err != nil || out != "" {
		t.Fatalf("nil-ask should be safe: out=%q err=%v", out, err)
	}
}
