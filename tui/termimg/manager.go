package termimg

import (
	"fmt"
	"sync"
)

// MaxLiveImages is the maximum number of images rendered as terminal
// graphics at one time. Images beyond this cap are demoted to a text
// placeholder to avoid unbounded graphics state in the terminal.
const MaxLiveImages = 8

// ImageRef carries the data needed to render one image inline.
type ImageRef struct {
	// ID is a stable identifier for the image across render passes.
	// It is used as the kitty image ID for transmit tracking.
	ID int
	// Data is the raw image bytes. For kitty, this must be PNG.
	Data []byte
	// MIME is the MIME type of the image (e.g. "image/png").
	MIME string
	// Source describes where the image came from (e.g.
	// "computer_use", "browser_vision", "read_image").
	Source string
}

// ImageManager tracks kitty transmit state and enforces the image
// budget. It is safe for concurrent use.
type ImageManager struct {
	protocol Protocol

	mu sync.Mutex
	// nextID is the next kitty image ID to assign.
	nextID int
	// transmitted tracks which image IDs have been sent to the
	// terminal (kitty only). iTerm2 has no transmit state.
	transmitted map[int]bool
	// displayOrder records the order images were observed in the
	// most recent render pass, for budget eviction.
	displayOrder []int
}

// NewImageManager creates an ImageManager for the detected protocol.
func NewImageManager() *ImageManager {
	return &ImageManager{
		protocol:    Detect(),
		transmitted: make(map[int]bool),
	}
}

// Protocol returns the active terminal graphics protocol.
func (m *ImageManager) Protocol() Protocol { return m.protocol }

// RenderImage returns the escape sequence to display an image at the
// given cell dimensions.
//
// For kitty on first render: emits transmit + place.
// For kitty on subsequent renders: emits place only.
// For iTerm2: always emits the full inline sequence.
//
// Returns ("", false) when the image was demoted to text fallback
// (budget exceeded). The caller should render a text placeholder.
func (m *ImageManager) RenderImage(ref ImageRef, cols, rows int) (seq string, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.protocol == ProtocolNone {
		return "", false
	}

	// Register in display order for budget tracking.
	m.displayOrder = append(m.displayOrder, ref.ID)

	if m.protocol == ProtocolITerm2 {
		return EncodeITerm2(ref.Data, ref.MIME, cols, rows), true
	}

	// Kitty: transmit-once + place.
	var b []byte
	first := !m.transmitted[ref.ID]
	if first {
		b = append(b, []byte(EncodeKittyTransmit(ref.Data, ref.ID))...)
		m.transmitted[ref.ID] = true
	}
	b = append(b, []byte(EncodeKittyPlace(ref.ID, cols, rows))...)
	return string(b), true
}

// BeginPass resets the display-order tracking for a new render pass.
// Call this before rendering all visible images.
func (m *ImageManager) BeginPass() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.displayOrder = m.displayOrder[:0]
}

// EvictedIDs returns image IDs that exceed the budget and should be
// purged from the terminal. Call after rendering all visible images
// (after BeginPass + all RenderImage calls).
//
// The oldest images beyond MaxLiveImages are evicted. Evicted images
// are removed from the transmitted set so they re-transmit if they
// become visible again.
func (m *ImageManager) EvictedIDs() []int {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.displayOrder) <= MaxLiveImages {
		return nil
	}

	// Keep the last MaxLiveImages (most recently observed); evict
	// the rest.
	cutoff := len(m.displayOrder) - MaxLiveImages
	evicted := make([]int, 0, cutoff)
	for i := 0; i < cutoff; i++ {
		id := m.displayOrder[i]
		if m.transmitted[id] {
			delete(m.transmitted, id)
			evicted = append(evicted, id)
		}
	}
	return evicted
}

// PurgeAll returns a sequence that deletes all transmitted kitty images
// from the terminal. Call on program exit to clean up graphics state.
func (m *ImageManager) PurgeAll() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.protocol != ProtocolKitty {
		return ""
	}

	var ids []int
	for id := range m.transmitted {
		ids = append(ids, id)
	}
	m.transmitted = make(map[int]bool)
	return EncodeKittyDeleteAll(ids)
}

// NextID returns the next available kitty image ID.
func (m *ImageManager) NextID() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	return m.nextID
}

// TextPlaceholder returns a text fallback for an image that cannot be
// rendered as terminal graphics.
func TextPlaceholder(width, height int) string {
	if width > 0 && height > 0 {
		return fmt.Sprintf("[Image: %dx%d]", width, height)
	}
	return "[Image]"
}
