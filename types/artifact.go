package types

import "time"

// Artifact is one entry of a session's artifact:// store in the form a
// recorded session keeps it: spilled tool output, or the full conversation a
// compaction summarized. ID is the N of artifact://N, so the references in a
// resumed context resolve to the same content.
type Artifact struct {
	ID      int64     `json:"id"`
	Tool    string    `json:"tool"`
	Created time.Time `json:"created"`
	Content string    `json:"content"`
}
