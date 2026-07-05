// Package attachstage holds the pure logic for combining CLI/TUI staged
// attachments with inline @path files from a slash action. It lives in its own
// package so both the cmd (CLI REPL) and tui frontends can reuse BuildSend
// without a cmd→tui dependency.
package attachstage

import (
	"github.com/mudler/nib/attachments"
	"github.com/mudler/nib/slash"
)

// StagedFile is a file staged via /attach, awaiting the next send.
type StagedFile struct {
	Path       string
	Transcribe bool
}

// BuildSend combines staged files (first) with the action's inline @path files,
// and builds the transcribe-only override map.
func BuildSend(pending []StagedFile, action slash.Action) ([]string, map[string]attachments.Override) {
	files := make([]string, 0, len(pending)+len(action.Files))
	overrides := map[string]attachments.Override{}
	for _, s := range pending {
		files = append(files, s.Path)
		if s.Transcribe {
			overrides[s.Path] = attachments.OverrideTranscribe
		}
	}
	files = append(files, action.Files...)
	return files, overrides
}
