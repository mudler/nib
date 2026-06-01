package hooks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/wiz/types"
)

func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func boolp(b bool) *bool { return &b }

func TestFireMatchingAndStdinEnv(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "h.sh",
		"cat > \"$WIZ_PLUGIN_ROOT/stdin.txt\"; echo \"$WIZ_PLUGIN_ROOT\" > \"$WIZ_PLUGIN_ROOT/root.txt\"; echo '{\"approved\": true}'")
	d := New([]types.HookConfig{{Event: "PreToolUse", Matcher: "bash", Command: script, Dir: dir}})

	if got := d.Fire(context.Background(), EventStop, "bash", map[string]any{"x": 1}); len(got) != 0 {
		t.Fatalf("non-matching event fired: %+v", got)
	}
	if got := d.Fire(context.Background(), EventPreToolUse, "other", nil); len(got) != 0 {
		t.Fatalf("non-matching matcher fired: %+v", got)
	}
	got := d.Fire(context.Background(), EventPreToolUse, "bash", map[string]any{"tool": "bash"})
	if len(got) != 1 || got[0].Approved == nil || !*got[0].Approved {
		t.Fatalf("expected one approve decision, got %+v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "stdin.txt")); len(b) == 0 {
		t.Fatal("hook did not receive payload on stdin")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "root.txt")); string(b) == "\n" || len(b) == 0 {
		t.Fatal("WIZ_PLUGIN_ROOT not set for the hook")
	}
}

func TestFireNonZeroExitBlocks(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "deny.sh", "echo 'no way' >&2; exit 2")
	d := New([]types.HookConfig{{Event: "PreToolUse", Command: script, Dir: dir}})
	got := d.Fire(context.Background(), EventPreToolUse, "bash", nil)
	if len(got) != 1 || !got[0].Block {
		t.Fatalf("non-zero exit should block: %+v", got)
	}
}

func TestCombineToolDecisions(t *testing.T) {
	td := CombineToolDecisions([]Decision{{Approved: boolp(true)}, {Block: true, Reason: "nope"}})
	if !td.Decided || td.Approve {
		t.Fatalf("block should deny: %+v", td)
	}
	td = CombineToolDecisions([]Decision{{Approved: boolp(true), Adjustment: "use -n"}})
	if !td.Decided || !td.Approve || td.Adjustment != "use -n" {
		t.Fatalf("should approve with adjustment: %+v", td)
	}
	td = CombineToolDecisions([]Decision{{}})
	if td.Decided {
		t.Fatalf("should be undecided: %+v", td)
	}
	td = CombineToolDecisions([]Decision{{Approved: boolp(false)}})
	if !td.Decided || td.Approve {
		t.Fatalf("approved:false should deny: %+v", td)
	}
}
