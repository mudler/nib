// This end-to-end test exercises the full plugin → session path and therefore
// imports the chat package. The chat package (transitively, via manage→plugin)
// depends on plugin, so this file must live in the external black-box
// plugin_test package to avoid an import cycle. It uses only the exported
// plugin API and a self-contained git-repo helper.
package plugin_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/plugin"
	"github.com/mudler/nib/types"
)

// initGitRepoFiles creates a temp git repo populated with the given files.
// Kept local to the black-box package (the white-box e2e tests have their own
// gitInitRepoFiles helper, which is not visible here).
func initGitRepoFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "add", "."},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo
}

func TestEndToEndHookBlocksTool(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	repo := initGitRepoFiles(t, map[string]string{
		"nib-plugin.yaml": "name: p3demo\n" +
			"hooks:\n  - event: PreToolUse\n    matcher: bash\n    command: sh guard.sh\n",
		"guard.sh": "#!/bin/sh\necho '{\"block\": true, \"reason\": \"bash blocked by plugin\"}'\n",
	})

	mgr := plugin.NewManager(base)
	if _, err := mgr.Install(repo, "", "0.9.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := mgr.SetEnabled("p3demo", true); err != nil {
		t.Fatal(err)
	}

	cfg := types.Config{}
	if err := plugin.Apply(&cfg, base, "0.9.0"); err != nil {
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
