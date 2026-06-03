package chat_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/config"
	"github.com/mudler/xlog"
)

// fakeOpenAI is a minimal OpenAI-compatible chat-completions endpoint that
// scripts one tool call per user turn. It supports both the non-streaming
// (chat.completion JSON) and streaming (SSE) paths, since cogito may use
// either. The Nth user turn is keyed by the count of role:"user" messages; once
// a tool result is fed back (last message role "tool"/"assistant"), it returns a
// plain content+stop message so the turn ends.
func fakeOpenAI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Stream   bool `json:"stream"`
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	body, _ := readAll(r)
	_ = json.Unmarshal(body, &req)

	users, lastRole := 0, ""
	for _, m := range req.Messages {
		if m.Role == "user" {
			users++
		}
		lastRole = m.Role
	}

	name, args, isTool := "", "", false
	if lastRole == "user" {
		switch users {
		case 1:
			name, args, isTool = "add_mcp_server", `{"name":"echo","command":"/bin/true","args":["x"],"env":["K=v"]}`, true
		case 2:
			name, args, isTool = "generate_skill", `{"name":"greet","description":"greets the user","instructions":"Say hello."}`, true
		case 3:
			name, args, isTool = "list_mcp_servers", `{}`, true
		case 4:
			name, args, isTool = "list_skills", `{}`, true
		}
	}

	if !req.Stream {
		msg := map[string]any{"role": "assistant"}
		finish := "stop"
		if isTool {
			msg["content"] = nil
			msg["tool_calls"] = []any{map[string]any{
				"id": fmt.Sprintf("call_%d", users), "type": "function", "index": 0,
				"function": map[string]any{"name": name, "arguments": args},
			}}
			finish = "tool_calls"
		} else {
			msg["content"] = "ok"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "fake", "object": "chat.completion", "model": "fake",
			"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
		})
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
		fmt.Fprintf(w, "data: %s\n\n", b)
		if fl != nil {
			fl.Flush()
		}
	}
	if isTool {
		emit(map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "id": fmt.Sprintf("call_%d", users), "type": "function",
			"function": map[string]any{"name": name, "arguments": args},
		}}}, "")
		emit(map[string]any{}, "tool_calls")
	} else {
		emit(map[string]any{"content": "ok"}, "")
		emit(map[string]any{}, "stop")
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	if fl != nil {
		fl.Flush()
	}
}

func readAll(r *http.Request) ([]byte, error) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return buf, nil
		}
	}
}

// TestSelfConfigToolsEndToEnd drives the real chat.Session against a fake LLM,
// exercising the full agent loop (tool schema generation, approval, execution,
// persistence, and per-turn reload) the way the binary does. It is the
// regression guard for the cogito-can't-schema-a-map panic that plain Run-based
// unit tests missed, and it proves plugins/skills/MCP-server config persist to
// disk and are visible to later turns.
func TestSelfConfigToolsEndToEnd(t *testing.T) {
	// Quiet cogito's debug logging (the binary sets this from cfg.LogLevel).
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	srv := httptest.NewServer(http.HandlerFunc(fakeOpenAI))
	defer srv.Close()

	// Isolate all on-disk state under a temp HOME; no XDG so plugin.BaseDir()
	// and config.WritablePath() both resolve to <home>/.config/nib.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("BASE_URL", srv.URL+"/v1") // defeat any ambient BASE_URL
	t.Setenv("MODEL", "fake-model")
	t.Setenv("API_KEY", "fake-key")

	cfgDir := filepath.Join(home, ".config", "nib")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	seed := "model: fake-model\napi_key: fake-key\nbase_url: " + srv.URL + "/v1\n" +
		"log_level: error\napproval_mode: auto\n"
	if err := os.WriteFile(cfgPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Load()
	if cfg.BaseURL != srv.URL+"/v1" || cfg.ApprovalMode != "auto" {
		t.Fatalf("config not loaded as expected: base=%q approval=%q", cfg.BaseURL, cfg.ApprovalMode)
	}

	var mu sync.Mutex
	results := map[string]string{}
	cb := chat.Callbacks{
		OnToolResult: func(res chat.ToolResult) {
			mu.Lock()
			results[res.Name] = res.Result
			mu.Unlock()
		},
	}

	session, err := chat.NewSession(context.Background(), cfg, cb)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	toolResult := func(name string) string {
		mu.Lock()
		defer mu.Unlock()
		return results[name]
	}

	// Turn 1: add an MCP server. Reaching this far already proves the tool
	// schemas build (the map-field panic regression).
	if _, err := session.SendMessage("add an mcp server"); err != nil {
		t.Fatalf("turn 1 (add_mcp_server): %v", err)
	}
	if got := toolResult("add_mcp_server"); !strings.Contains(got, "Added MCP server") {
		t.Fatalf("add_mcp_server result: %q", got)
	}
	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), "echo") || !strings.Contains(string(data), "mcp_servers") {
		t.Fatalf("mcp server not persisted to config:\n%s", data)
	}
	if !strings.Contains(string(data), "base_url") {
		t.Fatalf("config writer dropped pre-existing keys:\n%s", data)
	}

	// Turn 2: author a skill.
	if _, err := session.SendMessage("make a skill"); err != nil {
		t.Fatalf("turn 2 (generate_skill): %v", err)
	}
	if got := toolResult("generate_skill"); !strings.Contains(got, "greet") {
		t.Fatalf("generate_skill result: %q", got)
	}
	skillFile := filepath.Join(cfgDir, "skills", "local", "skills", "greet", "SKILL.md")
	if _, err := os.Stat(skillFile); err != nil {
		t.Fatalf("skill not written to disk: %v", err)
	}

	// Turn 3: a fresh decision turn runs applyPendingReload first; list the
	// servers and confirm the one added in turn 1 is visible.
	if _, err := session.SendMessage("list mcp servers"); err != nil {
		t.Fatalf("turn 3 (list_mcp_servers): %v", err)
	}
	if got := toolResult("list_mcp_servers"); !strings.Contains(got, "echo") {
		t.Fatalf("list_mcp_servers result: %q", got)
	}

	// Turn 4: the generated skill is registered and listed.
	if _, err := session.SendMessage("list skills"); err != nil {
		t.Fatalf("turn 4 (list_skills): %v", err)
	}
	if got := toolResult("list_skills"); !strings.Contains(got, "greet") {
		t.Fatalf("list_skills result: %q", got)
	}
}
