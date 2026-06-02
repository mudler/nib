package plugin

import (
	"path/filepath"
	"testing"
)

func TestLoadClaudeHooks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hooks", "hooks.json"), `{
  "hooks": {
    "PreToolUse": [
      { "matcher": "Bash", "hooks": [{"type": "command", "command": "guard.sh"}] }
    ],
    "PreCompact": [
      { "hooks": [{"type": "command", "command": "ignored.sh"}] }
    ]
  }
}`)
	hooks := loadClaudeHooks(dir)
	if len(hooks) != 1 {
		t.Fatalf("want 1 hook (PreCompact skipped), got %d: %+v", len(hooks), hooks)
	}
	if hooks[0].Event != "PreToolUse" || hooks[0].Matcher != "Bash" || hooks[0].Command != "guard.sh" {
		t.Fatalf("hook mapped wrong: %+v", hooks[0])
	}
}
