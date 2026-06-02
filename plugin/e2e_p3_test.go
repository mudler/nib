package plugin

import (
	"context"
	"os/exec"
	"testing"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

func TestEndToEndHookBlocksTool(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	repo := gitInitRepoFiles(t, map[string]string{
		"wiz-plugin.yaml": "name: p3demo\n" +
			"hooks:\n  - event: PreToolUse\n    matcher: bash\n    command: sh guard.sh\n",
		"guard.sh": "#!/bin/sh\necho '{\"block\": true, \"reason\": \"bash blocked by plugin\"}'\n",
	})

	mgr := NewManager(base)
	if _, err := mgr.Install(repo, "", "0.9.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := mgr.SetEnabled("p3demo", true); err != nil {
		t.Fatal(err)
	}

	cfg := types.Config{}
	if err := Apply(&cfg, base, "0.9.0"); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hooks) != 1 || cfg.Hooks[0].Event != "PreToolUse" || cfg.Hooks[0].Dir == "" {
		t.Fatalf("hook not merged with Dir stamped: %+v", cfg.Hooks)
	}

	s, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{
		OnToolCall: func(chat.ToolCallRequest) chat.ToolCallResponse {
			return chat.ToolCallResponse{Approved: true} // would approve, but the hook should pre-empt
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	if !s.ToolCallDenied(chat.ToolCallRequest{Name: "bash", Arguments: "{}"}) {
		t.Fatal("expected the plugin PreToolUse hook to deny the bash tool")
	}
}
