package termimg

import (
	"strings"
	"testing"
)

func TestImageManagerKittyTransmitOnce(t *testing.T) {
	t.Setenv("NIB_IMAGE_PROTOCOL", "kitty")
	t.Setenv("KITTY_WINDOW_ID", "1")

	m := NewImageManager()
	if m.Protocol() != ProtocolKitty {
		t.Fatalf("expected kitty protocol, got %s", m.Protocol())
	}

	ref := ImageRef{ID: 1, Data: []byte("png-data"), MIME: "image/png"}

	// First render: should contain transmit + place.
	m.BeginPass()
	seq1, ok := m.RenderImage(ref, 80, 24)
	if !ok {
		t.Fatal("first render should succeed")
	}
	if !strings.Contains(seq1, "a=t,") {
		t.Errorf("first render must contain transmit (a=t), got: %s", seq1[:min(30, len(seq1))])
	}
	if !strings.Contains(seq1, "a=p") {
		t.Errorf("first render must contain place (a=p)")
	}

	// Second render: should contain place only, no transmit.
	m.BeginPass()
	seq2, ok := m.RenderImage(ref, 80, 24)
	if !ok {
		t.Fatal("second render should succeed")
	}
	if strings.Contains(seq2, "a=t,") {
		t.Errorf("second render must NOT contain transmit (a=t)")
	}
	if !strings.Contains(seq2, "a=p") {
		t.Errorf("second render must contain place (a=p)")
	}
}

func TestImageManagerITerm2(t *testing.T) {
	t.Setenv("NIB_IMAGE_PROTOCOL", "iterm2")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("ITERM_SESSION_ID", "x")

	m := NewImageManager()
	if m.Protocol() != ProtocolITerm2 {
		t.Fatalf("expected iterm2 protocol, got %s", m.Protocol())
	}

	ref := ImageRef{ID: 1, Data: []byte("png-data"), MIME: "image/png"}

	m.BeginPass()
	seq, ok := m.RenderImage(ref, 80, 24)
	if !ok {
		t.Fatal("render should succeed")
	}
	if !strings.HasPrefix(seq, "\x1b]1337;File=") {
		t.Errorf("iTerm2 render must produce OSC 1337 sequence")
	}
}

func TestImageManagerNoneProtocol(t *testing.T) {
	t.Setenv("NIB_IMAGE_PROTOCOL", "off")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("ITERM_SESSION_ID", "")

	m := NewImageManager()
	if m.Protocol() != ProtocolNone {
		t.Fatalf("expected none protocol, got %s", m.Protocol())
	}

	ref := ImageRef{ID: 1, Data: []byte("png-data"), MIME: "image/png"}

	m.BeginPass()
	_, ok := m.RenderImage(ref, 80, 24)
	if ok {
		t.Error("none protocol should return ok=false")
	}
}

func TestImageManagerBudgetEviction(t *testing.T) {
	t.Setenv("NIB_IMAGE_PROTOCOL", "kitty")
	t.Setenv("KITTY_WINDOW_ID", "1")

	m := NewImageManager()

	// Render MaxLiveImages + 2 images to trigger eviction.
	m.BeginPass()
	for i := 1; i <= MaxLiveImages+2; i++ {
		ref := ImageRef{ID: i, Data: []byte("png"), MIME: "image/png"}
		m.RenderImage(ref, 80, 24)
	}

	evicted := m.EvictedIDs()
	if len(evicted) != 2 {
		t.Errorf("expected 2 evicted IDs, got %d", len(evicted))
	}
	// The oldest (ID 1 and 2) should be evicted.
	for _, id := range evicted {
		if id != 1 && id != 2 {
			t.Errorf("expected IDs 1 and 2 evicted, got %d", id)
		}
	}
}

func TestImageManagerPurgeAll(t *testing.T) {
	t.Setenv("NIB_IMAGE_PROTOCOL", "kitty")
	t.Setenv("KITTY_WINDOW_ID", "1")

	m := NewImageManager()

	// Transmit a few images.
	m.BeginPass()
	for i := 1; i <= 3; i++ {
		ref := ImageRef{ID: i, Data: []byte("png"), MIME: "image/png"}
		m.RenderImage(ref, 80, 24)
	}

	purge := m.PurgeAll()
	count := strings.Count(purge, "a=d")
	if count != 3 {
		t.Errorf("PurgeAll should contain 3 delete sequences, got %d", count)
	}
}

func TestImageManagerPurgeAllITerm2(t *testing.T) {
	t.Setenv("NIB_IMAGE_PROTOCOL", "iterm2")
	t.Setenv("ITERM_SESSION_ID", "x")

	m := NewImageManager()
	purge := m.PurgeAll()
	if purge != "" {
		t.Errorf("PurgeAll should be empty for iTerm2, got: %q", purge)
	}
}

func TestTextPlaceholder(t *testing.T) {
	s := TextPlaceholder(800, 600)
	if s != "[Image: 800x600]" {
		t.Errorf("TextPlaceholder(800,600) = %q, want %q", s, "[Image: 800x600]")
	}
	s = TextPlaceholder(0, 0)
	if s != "[Image]" {
		t.Errorf("TextPlaceholder(0,0) = %q, want %q", s, "[Image]")
	}
}

func TestNextID(t *testing.T) {
	t.Setenv("NIB_IMAGE_PROTOCOL", "kitty")
	t.Setenv("KITTY_WINDOW_ID", "1")

	m := NewImageManager()
	id1 := m.NextID()
	id2 := m.NextID()
	if id1 != 1 || id2 != 2 {
		t.Errorf("NextID should return 1 then 2, got %d, %d", id1, id2)
	}
}
