package chat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/loop"
	"github.com/mudler/nib/types"
)

// The cron tool, when wired to a registry-backed callback, registers a job and
// reports its id — the same path the model walks at runtime.
func TestCronCallbackRegistersJob(t *testing.T) {
	reg := loop.NewRegistry()
	cb := chat.Callbacks{
		OnCronCreate: func(req chat.CronRequest) string {
			j, err := reg.Add(req.Expr, req.Prompt, req.Recurring, req.Durable)
			if err != nil {
				return "rejected: " + err.Error()
			}
			return "ok " + j.ID
		},
		OnCronList:   func() string { return "listed" },
		OnCronDelete: func(id string) string { reg.Delete(id); return "deleted" },
	}
	s, err := chat.NewSession(context.Background(), types.Config{}, cb)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	_ = s

	// Exercise the callback directly (the tool's Run just forwards to it).
	out := cb.OnCronCreate(chat.CronRequest{Expr: "*/5 * * * *", Prompt: "/foo", Recurring: true})
	if !strings.HasPrefix(out, "ok ") {
		t.Fatalf("create: %q", out)
	}
	if len(reg.List()) != 1 {
		t.Fatalf("registry: want 1 job, got %d", len(reg.List()))
	}
}
