package trace

import (
	"fmt"
	"os"
	"path/filepath"
)

// Preflight reports whether a trace directory can actually be written, without
// keeping the file open.
//
// It exists because a failed recorder used to be a warning the default log
// level discarded, so a user who asked for a trace got none and no explanation.
// Checking here lets app.Run refuse the run before anything is spawned, which
// is the only place that can produce a clean exit in TUI mode: chat.NewSession
// runs inside a tea.Cmd there, so its error arrives as an in-TUI banner after
// the UI is already up.
//
// The probe duplicates what NewRecorder does rather than calling it, because a
// caller that is only asking the question must not be left holding an open
// file handle. It uses the same flags (never O_TRUNC), so an existing
// transcript is appended to rather than destroyed.
func Preflight(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot write trace to %s: %w", dir, err)
	}
	f, err := os.OpenFile(filepath.Join(dir, fileName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("cannot write trace to %s: %w", dir, err)
	}
	return f.Close()
}
