package mcp

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ArtifactStore is a session-scoped store for tool output that was too large
// to return inline. When a tool result exceeds the spill threshold, the full
// output is saved here and the model gets a head+tail slice plus an
// artifact://N reference it can page through with the read tool.
//
// Artifacts are in-memory and do not survive a session restart. They are
// addressed by a monotonically increasing ID: artifact://1, artifact://2, etc.
// The ID is assigned at save time and never reused.
type ArtifactStore struct {
	nextID atomic.Int64
	mu     sync.RWMutex
	items  map[int64]*Artifact
}

// Artifact is one piece of spilled tool output.
type Artifact struct {
	ID        int64
	Tool      string    // "bash", "read", "grep", etc.
	CreatedAt time.Time
	Content   string
}

// NewArtifactStore creates an empty artifact store.
func NewArtifactStore() *ArtifactStore {
	s := &ArtifactStore{items: make(map[int64]*Artifact)}
	s.nextID.Store(0)
	return s
}

// Save stores content as a new artifact and returns its artifact:// URI.
func (s *ArtifactStore) Save(tool, content string) string {
	id := s.nextID.Add(1)
	s.mu.Lock()
	s.items[id] = &Artifact{
		ID:        id,
		Tool:      tool,
		CreatedAt: time.Now(),
		Content:   content,
	}
	s.mu.Unlock()
	return ArtifactURI(id)
}

// Get retrieves an artifact by ID. Returns nil if not found.
func (s *ArtifactStore) Get(id int64) *Artifact {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Return a copy so callers can't mutate the stored artifact.
	a := s.items[id]
	if a == nil {
		return nil
	}
	cp := *a
	return &cp
}

// Count returns the number of stored artifacts.
func (s *ArtifactStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items)
}

// ArtifactURI formats an artifact ID as an artifact:// URI.
func ArtifactURI(id int64) string {
	// "artifact://1" — the read tool recognizes this path and serves
	// the stored content, with the same budget and paging as a file.
	return formatArtifactURI(id)
}

// SearchResult is a single match from ArtifactStore.Search.
type SearchResult struct {
	ID   int64  // artifact ID
	Line int    // 1-indexed line number of the match within the artifact
	Text string // rendered snippet: context lines + matching line + context lines
}

// Search returns matching lines from all artifacts, sorted by artifact ID then
// line number. The pattern is a Go regular expression, matched case-insensitive.
// Each result includes 3 lines of context before and after the matching line.
// Results are capped at 50 matches.
func (s *ArtifactStore) Search(pattern string) ([]SearchResult, error) {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Collect and sort artifact IDs for deterministic ordering.
	ids := make([]int64, 0, len(s.items))
	for id := range s.items {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	var results []SearchResult
	for _, id := range ids {
		if len(results) >= 50 {
			break
		}
		content := s.items[id].Content
		lines := strings.Split(content, "\n")
		for idx, line := range lines {
			if len(results) >= 50 {
				break
			}
			if !re.MatchString(line) {
				continue
			}
			lineNum := idx + 1 // 1-indexed

			startCtx := idx - 3
			if startCtx < 0 {
				startCtx = 0
			}
			endCtx := idx + 3
			if endCtx >= len(lines) {
				endCtx = len(lines) - 1
			}

			var b strings.Builder
			for j := startCtx; j <= endCtx; j++ {
				n := j + 1 // 1-indexed
				if j == idx {
					fmt.Fprintf(&b, "%d> %s\n", n, lines[j])
				} else {
					fmt.Fprintf(&b, "%d| %s\n", n, lines[j])
				}
			}

			results = append(results, SearchResult{
				ID:   id,
				Line: lineNum,
				Text: b.String(),
			})
		}
	}

	return results, nil
}
