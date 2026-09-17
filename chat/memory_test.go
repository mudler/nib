package chat

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMemoryStoreWriteAndRead(t *testing.T) {
	dir := t.TempDir()
	store := NewMemoryStore(dir)

	note := MemoryNote{Path: "architecture.md", Tags: []string{"design"}, Content: "Layered: tools + pruning + compaction."}
	if err := store.Write(note); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, ok := store.Read("architecture.md")
	if !ok {
		t.Fatal("Read returned false after Write")
	}
	if got.Content != note.Content {
		t.Fatalf("content = %q, want %q", got.Content, note.Content)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "design" {
		t.Fatalf("tags = %v, want [design]", got.Tags)
	}
	if got.Updated.IsZero() {
		t.Fatal("Updated was not set by Write")
	}
}

func TestMemoryStoreReadMissing(t *testing.T) {
	dir := t.TempDir()
	store := NewMemoryStore(dir)
	if _, ok := store.Read("nope.md"); ok {
		t.Fatal("Read returned true for a missing note")
	}
}

func TestMemoryStoreDelete(t *testing.T) {
	dir := t.TempDir()
	store := NewMemoryStore(dir)

	if err := store.Write(MemoryNote{Path: "temp.md", Content: "bye"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := store.Delete("temp.md"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := store.Read("temp.md"); ok {
		t.Fatal("note still readable after Delete")
	}
	// Deleting a missing note is not an error.
	if err := store.Delete("never-existed.md"); err != nil {
		t.Fatalf("Delete missing: %v", err)
	}
}

func TestMemoryStoreLoadMultiple(t *testing.T) {
	dir := t.TempDir()
	store := NewMemoryStore(dir)

	if err := store.Write(MemoryNote{Path: "a.md", Tags: []string{"x"}, Content: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(MemoryNote{Path: "b.md", Tags: []string{"y"}, Content: "B"}); err != nil {
		t.Fatal(err)
	}

	notes, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("len(notes) = %d, want 2", len(notes))
	}
	if notes["a.md"].Content != "A" || notes["b.md"].Content != "B" {
		t.Fatalf("notes = %+v", notes)
	}
}

func TestMemoryStoreLoadMissingDir(t *testing.T) {
	store := NewMemoryStore(filepath.Join(t.TempDir(), "does-not-exist"))
	notes, err := store.Load()
	if err != nil {
		t.Fatalf("Load on missing dir: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected empty map, got %d notes", len(notes))
	}
}

func TestMemoryStoreWriteCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", "memory")
	store := NewMemoryStore(dir)
	if err := store.Write(MemoryNote{Path: "first.md", Content: "hello"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("dir not created: %v", err)
	}
}

func TestMemoryToolWriteAndRead(t *testing.T) {
	store := NewMemoryStore(t.TempDir())
	tool := &memoryTool{store: store}

	out, _, err := tool.Run(map[string]any{
		"command": "write",
		"path":    "gotchas.md",
		"tags":    []any{"compaction", "pruning"},
		"content": "HighWaterTokens scales with utilization.",
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if out == "" {
		t.Fatal("write returned empty output")
	}

	out, _, err = tool.Run(map[string]any{
		"command": "read",
		"path":    "gotchas.md",
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !contains(out, "HighWaterTokens scales with utilization.") {
		t.Fatalf("read output = %q", out)
	}
	if !contains(out, "compaction") {
		t.Fatalf("read output missing tags: %q", out)
	}
}

func TestMemoryToolListByTags(t *testing.T) {
	store := NewMemoryStore(t.TempDir())
	tool := &memoryTool{store: store}

	tool.Run(map[string]any{"command": "write", "path": "a.md", "tags": []any{"x"}, "content": "A"})
	tool.Run(map[string]any{"command": "write", "path": "b.md", "tags": []any{"y"}, "content": "B"})
	tool.Run(map[string]any{"command": "write", "path": "c.md", "tags": []any{"x"}, "content": "C"})

	out, _, err := tool.Run(map[string]any{"command": "list", "tags": []any{"x"}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !contains(out, "a.md") || !contains(out, "c.md") {
		t.Fatalf("list by tag x = %q, want a.md and c.md", out)
	}
	if contains(out, "b.md") {
		t.Fatalf("list by tag x = %q, should not contain b.md", out)
	}
}

func TestMemoryToolDelete(t *testing.T) {
	store := NewMemoryStore(t.TempDir())
	tool := &memoryTool{store: store}

	tool.Run(map[string]any{"command": "write", "path": "temp.md", "content": "bye"})
	out, _, err := tool.Run(map[string]any{"command": "delete", "path": "temp.md"})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !contains(out, "Deleted") {
		t.Fatalf("delete output = %q", out)
	}
	if _, ok := store.Read("temp.md"); ok {
		t.Fatal("note still readable after delete")
	}
}

func TestMemoryToolWriteMissingPath(t *testing.T) {
	store := NewMemoryStore(t.TempDir())
	tool := &memoryTool{store: store}
	out, _, err := tool.Run(map[string]any{"command": "write", "content": "no path"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(out, "error") {
		t.Fatalf("expected error in output, got %q", out)
	}
}

func TestMemoryToolReadByTags(t *testing.T) {
	store := NewMemoryStore(t.TempDir())
	tool := &memoryTool{store: store}

	tool.Run(map[string]any{"command": "write", "path": "a.md", "tags": []any{"design"}, "content": "AAA"})
	tool.Run(map[string]any{"command": "write", "path": "b.md", "tags": []any{"design"}, "content": "BBB"})
	tool.Run(map[string]any{"command": "write", "path": "c.md", "tags": []any{"bug"}, "content": "CCC"})

	out, _, err := tool.Run(map[string]any{"command": "read", "tags": []any{"design"}})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !contains(out, "AAA") || !contains(out, "BBB") {
		t.Fatalf("read by tags = %q, want both AAA and BBB", out)
	}
	if contains(out, "CCC") {
		t.Fatalf("read by tags = %q, should not contain CCC", out)
	}
}

func TestMemoryToolUnknownCommand(t *testing.T) {
	store := NewMemoryStore(t.TempDir())
	tool := &memoryTool{store: store}
	out, _, err := tool.Run(map[string]any{"command": "frobnicate"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(out, "Unknown command") {
		t.Fatalf("expected Unknown command in output, got %q", out)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
