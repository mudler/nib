package chat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/nib/hooks"
	"github.com/mudler/nib/types"
)

// Use the real SendMessage wiring, including permission decisions and hooks.
func TestSessionToolLifecycleExactlyOnce(t *testing.T) {
	for _, mode := range []string{"completed", "denied", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			s := newWarmTestSession(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.ctx = ctx
			dir := t.TempDir()
			s.workingDir = dir
			if err := os.WriteFile(filepath.Join(dir, "input"), []byte("hello"), 0600); err != nil {
				t.Fatal(err)
			}
			s.toolAllow = map[string]bool{"agent_logs": true}
			s.llm = &policyCallLLM{name: "agent_logs", args: `{"agent_id":"missing"}`}
			s.hooks = hooks.New([]types.HookConfig{{Event: "PostToolUse", Command: "echo post >> post", Dir: dir}})
			var queued, started int
			var results []ToolResult
			s.callbacks = Callbacks{
				OnToolQueued: func(ts ToolStart) {
					queued++
					if ts.ID != "policy-call" {
						t.Error("lost queued identity")
					}
				},
				ToolPolicy: func(ToolCallRequest) error {
					if mode == "cancelled" {
						cancel()
					}
					if mode == "denied" {
						return errors.New("denied")
					}
					return nil
				},
				OnToolStart:  func(ToolStart) { started++ },
				OnToolResult: func(r ToolResult) { results = append(results, r) },
			}
			_, err := s.SendMessage("read")
			if mode == "completed" && err != nil {
				t.Fatal(err)
			}
			if queued != 1 || len(results) != 1 {
				t.Fatalf("queued=%d results=%+v err=%v", queued, results, err)
			}
			if results[0].Outcome != mode || results[0].ID != "policy-call" || results[0].AgentID != "" {
				t.Fatalf("terminal: %+v", results[0])
			}
			data, _ := os.ReadFile(filepath.Join(dir, "post"))
			if mode == "completed" {
				if started != 1 || string(data) != "post\n" {
					t.Fatalf("starts=%d hooks=%q", started, data)
				}
			} else if started != 0 || len(data) != 0 {
				t.Fatalf("unexecuted call: starts=%d hooks=%q", started, data)
			}
		})
	}
}
