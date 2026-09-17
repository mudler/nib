package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MemoryNote is one persisted note: a path (relative filename), tags for
// retrieval, a body, and the last-write timestamp.
type MemoryNote struct {
	Path    string    `json:"path"`
	Tags    []string  `json:"tags"`
	Content string    `json:"content"`
	Updated time.Time `json:"updated"`
}

// MemoryStore persists MemoryNotes as individual JSON files under Dir. One
// file per note keeps a corrupt write from poisoning the whole store, and
// atomic writes (temp + rename in the same directory) keep individual notes
// crash-safe. Dir is created on first Write; Load and Read tolerate it not
// existing yet.
type MemoryStore struct {
	Dir string
}

// NewMemoryStore returns a store rooted at dir. dir is not created until the
// first Write.
func NewMemoryStore(dir string) *MemoryStore {
	return &MemoryStore{Dir: dir}
}

// Load reads all notes from the store directory. A missing directory is not
// an error: it returns an empty map.
func (m *MemoryStore) Load() (map[string]MemoryNote, error) {
	notes := map[string]MemoryNote{}
	entries, err := os.ReadDir(m.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return notes, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(m.Dir, e.Name()))
		if err != nil {
			continue
		}
		var n MemoryNote
		if err := json.Unmarshal(data, &n); err != nil {
			continue
		}
		if n.Path != "" {
			notes[n.Path] = n
		}
	}
	return notes, nil
}

// Write saves a note atomically: marshal to a temp file in the same directory,
// then rename over the destination. The same-directory requirement is what
// makes os.Rename atomic — a temp file elsewhere could be on a different
// filesystem.
func (m *MemoryStore) Write(n MemoryNote) error {
	if n.Path == "" {
		return fmt.Errorf("memory note path is empty")
	}
	if err := os.MkdirAll(m.Dir, 0o755); err != nil {
		return err
	}
	n.Updated = time.Now().UTC()
	data, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(m.Dir, ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	tmp.Close()
	return os.Rename(tmp.Name(), m.notePath(n.Path))
}

// Read returns one note by path. The second return is false when the note
// does not exist or cannot be parsed.
func (m *MemoryStore) Read(path string) (MemoryNote, bool) {
	data, err := os.ReadFile(m.notePath(path))
	if err != nil {
		return MemoryNote{}, false
	}
	var n MemoryNote
	if err := json.Unmarshal(data, &n); err != nil {
		return MemoryNote{}, false
	}
	return n, true
}

// Delete removes a note by path. A missing note is not an error.
func (m *MemoryStore) Delete(path string) error {
	err := os.Remove(m.notePath(path))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// notePath returns the file path for a note. Path separators in the note path
// are replaced with underscores so a single path segment never spans
// directories.
func (m *MemoryStore) notePath(path string) string {
	safe := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(path)
	return filepath.Join(m.Dir, safe+".json")
}

// formatNote renders a single note for tool output.
func formatNote(n MemoryNote) string {
	tags := strings.Join(n.Tags, ", ")
	if tags == "" {
		tags = "(none)"
	}
	return fmt.Sprintf("# %s\nTags: %s\nUpdated: %s\n\n%s",
		n.Path, tags, n.Updated.Format("2006-01-02 15:04 MST"), n.Content)
}

// filterByTags returns notes that carry at least one of the given tags. An
// empty tag list returns all notes.
func filterByTags(notes map[string]MemoryNote, tags []string) []MemoryNote {
	if len(tags) == 0 {
		out := make([]MemoryNote, 0, len(notes))
		for _, n := range notes {
			out = append(out, n)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
		return out
	}
	tagSet := map[string]bool{}
	for _, t := range tags {
		tagSet[t] = true
	}
	var out []MemoryNote
	for _, n := range notes {
		for _, t := range n.Tags {
			if tagSet[t] {
				out = append(out, n)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
