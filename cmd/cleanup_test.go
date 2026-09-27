package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mudler/nib/chat"
)

func TestParseAge(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"30d": 30 * 24 * time.Hour,
		"2w":  14 * 24 * time.Hour,
		"12h": 12 * time.Hour,
	} {
		if got, err := parseAge(in); err != nil || got != want {
			t.Errorf("parseAge(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "d", "-3d", "0d", "soon", "-1h"} {
		if _, err := parseAge(in); err == nil {
			t.Errorf("parseAge(%q) accepted", in)
		}
	}
}

func TestSelectForCleanup(t *testing.T) {
	now := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	sessions := []chat.SessionRecord{ // newest first
		{ID: "a", Updated: now.Add(-1 * time.Hour)},
		{ID: "b", Updated: now.Add(-48 * time.Hour)},
		{ID: "c", Updated: now.Add(-40 * 24 * time.Hour)},
	}
	ids := func(rs []chat.SessionRecord) string {
		var out []string
		for _, r := range rs {
			out = append(out, r.ID)
		}
		return strings.Join(out, ",")
	}
	cases := []struct {
		name string
		opts cleanupOptions
		want string
	}{
		{"none", cleanupOptions{keep: -1}, ""},
		{"all", cleanupOptions{all: true, keep: -1}, "a,b,c"},
		{"older than 30d", cleanupOptions{olderThan: 30 * 24 * time.Hour, keep: -1}, "c"},
		{"keep 1", cleanupOptions{keep: 1}, "b,c"},
		{"keep 0", cleanupOptions{keep: 0}, "a,b,c"},
		{"either selector", cleanupOptions{olderThan: 24 * time.Hour, keep: 2}, "b,c"},
	}
	for _, c := range cases {
		if got := ids(selectForCleanup(sessions, now, c.opts)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRunCleanupDeletesAndDryRuns(t *testing.T) {
	dir := t.TempDir()
	store := chat.NewSessionStore(dir)
	now := time.Now()
	for id, updated := range map[string]time.Time{"old": now.Add(-60 * 24 * time.Hour), "new": now} {
		if err := store.Save(chat.SessionRecord{ID: id, Title: id, Updated: updated}); err != nil {
			t.Fatal(err)
		}
	}
	stale := filepath.Join(dir, ".old-123.tmp")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * staleTempAge)
	_ = os.Chtimes(stale, past, past)

	var out, errOut bytes.Buffer
	if code := runCleanup("nib", dir, []string{"--older-than", "30d", "--dry-run"}, time.Now(), &out, &errOut); code != 0 {
		t.Fatalf("dry run exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "would delete old") {
		t.Fatalf("dry run output = %q", out.String())
	}
	if _, err := os.Stat(store.Path("old")); err != nil {
		t.Fatalf("dry run deleted the session: %v", err)
	}

	out.Reset()
	if code := runCleanup("nib", dir, []string{"--older-than", "30d"}, time.Now(), &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if _, err := os.Stat(store.Path("old")); !os.IsNotExist(err) {
		t.Fatalf("old session still there: %v", err)
	}
	if _, err := os.Stat(store.Path("new")); err != nil {
		t.Fatalf("new session deleted: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale temp file still there: %v", err)
	}
	if !strings.Contains(out.String(), "Deleted 1 of 2 sessions") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunCleanupWithoutSelectorDeletesNothing(t *testing.T) {
	dir := t.TempDir()
	store := chat.NewSessionStore(dir)
	if err := store.Save(chat.SessionRecord{ID: "s"}); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runCleanup("nib", dir, nil, time.Now(), &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "1 recorded sessions") {
		t.Fatalf("output = %q", out.String())
	}
	if _, err := os.Stat(store.Path("s")); err != nil {
		t.Fatalf("session deleted without a selector: %v", err)
	}
}
