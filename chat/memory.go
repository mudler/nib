package chat

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mudler/cogito"
)

// memoryArgs is the JSON-schema shape of the memory tool's parameters.
type memoryArgs struct {
	Command string   `json:"command" jsonschema:"memory operation: list, read, write, or delete"`
	Path    string   `json:"path,omitempty" jsonschema:"note path (relative filename). Required for write, read by path, and delete."`
	Tags    []string `json:"tags,omitempty" jsonschema:"tags for the note (write) or filter (list, read). snake_case."`
	Content string   `json:"content,omitempty" jsonschema:"note body (write only). Keep entries concise and current."`
}

// memoryTool is a cogito tool that reads and writes persistent notes via a
// MemoryStore. It satisfies cogito.Tool[map[string]any].
type memoryTool struct {
	store *MemoryStore
}

// Run dispatches the requested command against the store.
func (m *memoryTool) Run(args map[string]any) (string, any, error) {
	cmd, _ := args["command"].(string)
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	var tags []string
	switch v := args["tags"].(type) {
	case []any:
		for _, t := range v {
			if s, ok := t.(string); ok {
				tags = append(tags, s)
			}
		}
	case []string:
		tags = v
	}

	switch cmd {
	case "write":
		if path == "" {
			return "error: path is required for write", nil, nil
		}
		if content == "" {
			return "error: content is required for write", nil, nil
		}
		if err := m.store.Write(MemoryNote{Path: path, Tags: tags, Content: content}); err != nil {
			return "", nil, fmt.Errorf("write memory note: %w", err)
		}
		return fmt.Sprintf("Saved memory note: %s", path), nil, nil

	case "read":
		if path != "" {
			note, ok := m.store.Read(path)
			if !ok {
				return fmt.Sprintf("No memory note at path: %s", path), nil, nil
			}
			return formatNote(note), nil, nil
		}
		notes, err := m.store.Load()
		if err != nil {
			return "", nil, fmt.Errorf("read memory: %w", err)
		}
		filtered := filterByTags(notes, tags)
		if len(filtered) == 0 {
			return "No memory notes matching those tags.", nil, nil
		}
		var b strings.Builder
		for i, n := range filtered {
			if i > 0 {
				b.WriteString("\n---\n")
			}
			b.WriteString(formatNote(n))
		}
		return b.String(), nil, nil

	case "list":
		notes, err := m.store.Load()
		if err != nil {
			return "", nil, fmt.Errorf("list memory: %w", err)
		}
		filtered := filterByTags(notes, tags)
		if len(filtered) == 0 {
			return "No memory notes.", nil, nil
		}
		byTag := map[string][]string{}
		for _, n := range filtered {
			if len(n.Tags) == 0 {
				byTag["untagged"] = append(byTag["untagged"], n.Path)
				continue
			}
			for _, t := range n.Tags {
				byTag[t] = append(byTag[t], n.Path)
			}
		}
		var b strings.Builder
		for _, tag := range sortedTagKeys(byTag) {
			b.WriteString(fmt.Sprintf("# %s\n", tag))
			for _, p := range byTag[tag] {
				b.WriteString(fmt.Sprintf("  %s\n", p))
			}
		}
		return b.String(), nil, nil

	case "delete":
		if path == "" {
			return "error: path is required for delete", nil, nil
		}
		if err := m.store.Delete(path); err != nil {
			return "", nil, fmt.Errorf("delete memory note: %w", err)
		}
		return fmt.Sprintf("Deleted memory note: %s", path), nil, nil

	default:
		return fmt.Sprintf("Unknown command %q. Use list, read, write, or delete.", cmd), nil, nil
	}
}

// memoryToolDefinition builds the cogito tool definition for the memory tool.
func memoryToolDefinition(store *MemoryStore) cogito.ToolDefinitionInterface {
	return cogito.NewToolDefinition[map[string]any](
		&memoryTool{store: store},
		memoryArgs{},
		"memory",
		"Persistent project-scoped scratchpad for learnings, patterns, decisions, and gotchas across sessions. "+
			"Notes survive compaction and model restarts. Use `list [tags]` for a tag-grouped index, "+
			"`read` with a path to get one note or with tags to get collated bodies, "+
			"`write path tags content` to create or overwrite a note, and `delete path` to remove one. "+
			"Save important context before it is lost to compaction.",
	)
}

func sortedTagKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
