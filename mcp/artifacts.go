package mcp

import (
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
