package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteUsageWritesJSON(t *testing.T) {
	dir := t.TempDir()
	type usage struct {
		TotalTokens int `json:"total_tokens"`
		Turns       int `json:"turns"`
	}
	if err := WriteUsage(dir, usage{TotalTokens: 330800, Turns: 14}); err != nil {
		t.Fatalf("WriteUsage: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "usage.json"))
	if err != nil {
		t.Fatalf("usage.json missing: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("usage.json is not valid JSON: %v", err)
	}
	if got["turns"] != float64(14) {
		t.Fatalf("turns = %v, want 14", got["turns"])
	}
}

// The transcript is a separate contract and must not be touched.
func TestWriteUsageLeavesTheTranscriptAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)
	if err := os.WriteFile(path, []byte("{\"a\":1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteUsage(dir, map[string]int{"turns": 1}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{\"a\":1}\n" {
		t.Fatalf("WriteUsage modified trace.ndjson: %q", data)
	}
}

// A second write replaces the first rather than appending: Close can run more
// than once, and a harness must never read a file with two JSON objects in it.
func TestWriteUsageReplacesAPreviousReport(t *testing.T) {
	dir := t.TempDir()
	if err := WriteUsage(dir, map[string]int{"turns": 1}); err != nil {
		t.Fatal(err)
	}
	if err := WriteUsage(dir, map[string]int{"turns": 2}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, usageFileName))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]int
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("usage.json is not a single JSON object: %v (%q)", err, data)
	}
	if got["turns"] != 2 {
		t.Fatalf("turns = %d, want 2", got["turns"])
	}
}

// A missing directory is an error the caller sees, not a panic.
func TestWriteUsageReportsAMissingDir(t *testing.T) {
	if err := WriteUsage(filepath.Join(t.TempDir(), "nope"), map[string]int{"turns": 1}); err == nil {
		t.Fatal("WriteUsage into a missing dir: want error, got nil")
	}
}
