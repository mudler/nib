package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

var benchmarkDraftProjection draftProjection
var benchmarkComposerView string

// Compare equal segment counts with growing hidden payloads. Input construction
// is outside timing; ingestion includes the rune-to-string conversion in Update.
func BenchmarkComposerPaste(b *testing.B) {
	for _, tc := range []struct {
		name    string
		repeats int
	}{
		{"8KiB", 1024}, {"128KiB", 16384}, {"1MiB", 131072},
	} {
		payload := strings.Repeat("界\t\r\nxy", tc.repeats) // eight UTF-8 bytes per unit
		b.Run(tc.name, func(b *testing.B) {
			b.Run("Ingestion", func(b *testing.B) {
				key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(payload), Paste: true}
				m := newQueueTestModel()
				b.ReportAllocs()
				b.SetBytes(int64(len(payload)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					m.clearComposer()
					m = update(m, key)
				}
			})
			b.Run("CachedProjection", func(b *testing.B) {
				var d inputDraft
				if _, err := d.Replace(0, 0, payload, true); err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					benchmarkDraftProjection = d.Projection()
				}
			})
			b.Run("Redraw", func(b *testing.B) {
				m := newQueueTestModel()
				m = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})
				m = composerPaste(m, payload)
				_ = m.View() // warm layout/render caches
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					benchmarkComposerView = m.View()
				}
			})
		})
	}
}
