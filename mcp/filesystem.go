package mcp

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fileSystem holds per-server state that gates mutating operations. An edit is
// only allowed once the agent has seen the file's contents, either by reading
// it or by writing it. This prevents blind edits against files the agent has
// never inspected.
type fileSystem struct {
	mu   sync.Mutex
	seen map[string]bool
}

// newFileSystem creates a filesystem handler with an empty seen-file set.
func newFileSystem() *fileSystem {
	return &fileSystem{seen: make(map[string]bool)}
}

// pathKey normalizes a path so the same file is recognized regardless of how
// it was spelled (relative vs. absolute, redundant "." segments, etc.).
func pathKey(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

// markSeen records that the agent has observed a file's contents.
func (f *fileSystem) markSeen(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen[pathKey(path)] = true
}

// hasSeen reports whether the agent has observed a file's contents.
func (f *fileSystem) hasSeen(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seen[pathKey(path)]
}

// read reads a file and records it as seen so it can later be edited.
func (f *fileSystem) read(ctx context.Context, req *mcp.CallToolRequest, input readFileInput) (
	*mcp.CallToolResult,
	readFileOutput,
	error,
) {
	res, out, err := readFile(ctx, req, input)
	if out.Success {
		f.markSeen(input.Path)
	}
	return res, out, err
}

// write writes a file and records it as seen: after writing, the agent knows
// the file's exact contents, so a subsequent edit is no longer blind.
func (f *fileSystem) write(ctx context.Context, req *mcp.CallToolRequest, input writeFileInput) (
	*mcp.CallToolResult,
	writeFileOutput,
	error,
) {
	res, out, err := writeFile(ctx, req, input)
	if out.Success {
		f.markSeen(input.Path)
	}
	return res, out, err
}

// edit replaces a string in a file, but only if the file was previously read
// or written; otherwise it refuses, since editing an unseen file is unsafe.
func (f *fileSystem) edit(ctx context.Context, req *mcp.CallToolRequest, input editFileInput) (
	*mcp.CallToolResult,
	editFileOutput,
	error,
) {
	if !f.hasSeen(input.Path) {
		return nil, editFileOutput{
			Success: false,
			Error:   "file must be read before editing; call read on this path first",
		}, nil
	}
	return editFile(ctx, req, input)
}

// Input type for reading files
type readFileInput struct {
	Path   string `json:"path" jsonschema:"the file path to read"`
	Offset int    `json:"offset,omitempty" jsonschema:"optional line offset to start reading from (0-based)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"optional maximum number of lines to read"`
}

// Output type for read operation
type readFileOutput struct {
	Content    string `json:"content" jsonschema:"file content with line numbers in format '   1| content'"`
	TotalLines int    `json:"total_lines" jsonschema:"total number of lines in file"`
	Success    bool   `json:"success" jsonschema:"whether operation was successful"`
	Error      string `json:"error,omitempty" jsonschema:"error message if failed"`
}

// Input type for writing files
type writeFileInput struct {
	Path    string `json:"path" jsonschema:"the file path to write to"`
	Content string `json:"content" jsonschema:"the content to write"`
}

// Output type for write operation
type writeFileOutput struct {
	Success bool   `json:"success" jsonschema:"whether operation was successful"`
	Error   string `json:"error,omitempty" jsonschema:"error message if failed"`
}

// Input type for editing files
type editFileInput struct {
	Path string `json:"path" jsonschema:"the file path to edit"`
	Old  string `json:"old" jsonschema:"the old string to replace"`
	New  string `json:"new" jsonschema:"the new string to replace with"`
	All  bool   `json:"all,omitempty" jsonschema:"optional replace all occurrences (default: false)"`
}

// Output type for edit operation
type editFileOutput struct {
	Replacements int    `json:"replacements" jsonschema:"number of replacements made"`
	Success      bool   `json:"success" jsonschema:"whether operation was successful"`
	Error        string `json:"error,omitempty" jsonschema:"error message if failed"`
}

// Input type for glob operation
type globFilesInput struct {
	Pat  string `json:"pat" jsonschema:"the glob pattern to match files"`
	Path string `json:"path,omitempty" jsonschema:"optional base path (default: '.')"`
}

// Output type for glob operation
type globFilesOutput struct {
	Files   []string `json:"files" jsonschema:"list of matching files"`
	Count   int      `json:"count" jsonschema:"number of files found"`
	Success bool     `json:"success" jsonschema:"whether operation was successful"`
	Error   string   `json:"error,omitempty" jsonschema:"error message if failed"`
}

// Input type for grep operation
type grepFilesInput struct {
	Pat  string `json:"pat" jsonschema:"the regex pattern to search for"`
	Path string `json:"path,omitempty" jsonschema:"optional base path (default: '.')"`
}

// Output type for grep operation
type grepFilesOutput struct {
	Matches []string `json:"matches" jsonschema:"list of matches in format 'filepath:line_number:content'"`
	Count   int      `json:"count" jsonschema:"number of matches found"`
	Success bool     `json:"success" jsonschema:"whether operation was successful"`
	Error   string   `json:"error,omitempty" jsonschema:"error message if failed"`
}

// readFile reads a file with optional offset and limit
func readFile(ctx context.Context, req *mcp.CallToolRequest, input readFileInput) (
	*mcp.CallToolResult,
	readFileOutput,
	error,
) {
	// Read file content
	file, err := os.Open(input.Path)
	if err != nil {
		return nil, readFileOutput{
			Success: false,
			Error:   err.Error(),
		}, nil
	}
	defer file.Close()

	// Read all lines
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return nil, readFileOutput{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	totalLines := len(lines)

	// Apply offset and limit
	offset := input.Offset
	if offset < 0 {
		offset = 0
	}

	// Return empty content if offset is beyond file length
	if offset >= totalLines {
		return nil, readFileOutput{
			Content:    "",
			TotalLines: totalLines,
			Success:    true,
		}, nil
	}

	// Calculate end index
	endIndex := totalLines
	if input.Limit > 0 {
		endIndex = offset + input.Limit
		if endIndex > totalLines {
			endIndex = totalLines
		}
	}

	// Calculate width for line numbers based on total lines
	width := len(fmt.Sprintf("%d", totalLines))
	if width < 4 {
		width = 4 // Minimum width of 4 for consistency
	}

	// Format lines with line numbers (right-aligned)
	var formattedLines []string
	for i := offset; i < endIndex; i++ {
		formattedLines = append(formattedLines, fmt.Sprintf("%*d| %s", width, i+1, lines[i]))
	}

	content := strings.Join(formattedLines, "\n")

	return nil, readFileOutput{
		Content:    content,
		TotalLines: totalLines,
		Success:    true,
	}, nil
}

// writeFile writes content to a file
func writeFile(ctx context.Context, req *mcp.CallToolRequest, input writeFileInput) (
	*mcp.CallToolResult,
	writeFileOutput,
	error,
) {
	// Create parent directories if needed
	dir := filepath.Dir(input.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, writeFileOutput{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	// Write file
	if err := os.WriteFile(input.Path, []byte(input.Content), 0644); err != nil {
		return nil, writeFileOutput{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return nil, writeFileOutput{
		Success: true,
	}, nil
}

// editFile replaces old string with new string in a file
func editFile(ctx context.Context, req *mcp.CallToolRequest, input editFileInput) (
	*mcp.CallToolResult,
	editFileOutput,
	error,
) {
	// Read file content
	content, err := os.ReadFile(input.Path)
	if err != nil {
		return nil, editFileOutput{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	contentStr := string(content)

	// Count occurrences
	count := strings.Count(contentStr, input.Old)

	if count == 0 {
		return nil, editFileOutput{
			Success: false,
			Error:   "old string not found in file",
		}, nil
	}

	if count > 1 && !input.All {
		return nil, editFileOutput{
			Success: false,
			Error:   fmt.Sprintf("old string appears %d times in file, use all=true to replace all occurrences", count),
		}, nil
	}

	// Replace
	var newContent string
	if input.All {
		newContent = strings.ReplaceAll(contentStr, input.Old, input.New)
	} else {
		newContent = strings.Replace(contentStr, input.Old, input.New, 1)
	}

	// Write back
	if err := os.WriteFile(input.Path, []byte(newContent), 0644); err != nil {
		return nil, editFileOutput{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return nil, editFileOutput{
		Replacements: count,
		Success:      true,
	}, nil
}

// globFiles finds files by glob pattern
func globFiles(ctx context.Context, req *mcp.CallToolRequest, input globFilesInput) (
	*mcp.CallToolResult,
	globFilesOutput,
	error,
) {
	basePath := input.Path
	if basePath == "" {
		basePath = "."
	}

	var matches []string

	// Check if pattern contains ** for recursive matching
	if strings.Contains(input.Pat, "**") {
		// Use WalkDir for recursive matching
		// Extract the pattern after ** for matching
		patternParts := strings.Split(input.Pat, "**")
		var suffix string
		if len(patternParts) > 1 {
			suffix = strings.TrimPrefix(patternParts[1], "/")
		}

		err := filepath.WalkDir(basePath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // Skip errors
			}

			// Skip directories
			if d.IsDir() {
				return nil
			}

			// Get relative path for matching
			relPath, err := filepath.Rel(basePath, path)
			if err != nil {
				return nil
			}

			// Match against the suffix pattern
			var matched bool
			if suffix != "" {
				// Match the end of the path against the suffix pattern
				matched, _ = filepath.Match(suffix, filepath.Base(relPath))
				// If the pattern contains directory separators, match the full path
				if !matched && strings.Contains(suffix, string(filepath.Separator)) {
					matched, _ = filepath.Match(suffix, relPath)
				}
			} else {
				// If no suffix, match all files
				matched = true
			}

			if matched {
				// Clean and normalize path
				cleanPath := filepath.Clean(path)
				matches = append(matches, cleanPath)
			}

			return nil
		})

		if err != nil {
			return nil, globFilesOutput{
				Success: false,
				Error:   err.Error(),
			}, nil
		}
	} else {
		// Use standard glob for simple patterns
		pattern := filepath.Join(basePath, input.Pat)
		files, err := filepath.Glob(pattern)
		if err != nil {
			return nil, globFilesOutput{
				Success: false,
				Error:   err.Error(),
			}, nil
		}

		// Filter out directories
		for _, file := range files {
			info, err := os.Stat(file)
			if err != nil {
				continue
			}
			if !info.IsDir() {
				matches = append(matches, file)
			}
		}
	}

	// Sort by modification time (newest first)
	type fileWithTime struct {
		path    string
		modTime time.Time
	}

	filesWithTime := make([]fileWithTime, 0, len(matches))
	for _, file := range matches {
		info, err := os.Stat(file)
		if err != nil {
			continue
		}
		filesWithTime = append(filesWithTime, fileWithTime{
			path:    file,
			modTime: info.ModTime(),
		})
	}

	sort.Slice(filesWithTime, func(i, j int) bool {
		return filesWithTime[i].modTime.After(filesWithTime[j].modTime)
	})

	// Extract sorted file paths
	sortedFiles := make([]string, 0, len(filesWithTime))
	for _, f := range filesWithTime {
		sortedFiles = append(sortedFiles, f.path)
	}

	return nil, globFilesOutput{
		Files:   sortedFiles,
		Count:   len(sortedFiles),
		Success: true,
	}, nil
}

// searchFileForPattern searches a file for regex pattern matches
func searchFileForPattern(path string, re *regexp.Regexp, maxMatches int) []string {
	var matches []string

	file, err := os.Open(path)
	if err != nil {
		return matches // Skip files that can't be opened
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNum := 1
	for scanner.Scan() {
		if len(matches) >= maxMatches {
			break
		}

		line := scanner.Text()
		if re.MatchString(line) {
			match := fmt.Sprintf("%s:%d:%s", path, lineNum, strings.TrimSpace(line))
			matches = append(matches, match)
		}
		lineNum++
	}

	return matches
}

// grepFiles searches files for regex pattern
func grepFiles(ctx context.Context, req *mcp.CallToolRequest, input grepFilesInput) (
	*mcp.CallToolResult,
	grepFilesOutput,
	error,
) {
	// Compile regex
	re, err := regexp.Compile(input.Pat)
	if err != nil {
		return nil, grepFilesOutput{
			Success: false,
			Error:   fmt.Sprintf("invalid regex pattern: %s", err.Error()),
		}, nil
	}

	basePath := input.Path
	if basePath == "" {
		basePath = "."
	}

	var matches []string
	const maxMatches = 50

	// Walk directory tree
	err = filepath.WalkDir(basePath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		// Limit matches
		if len(matches) >= maxMatches {
			return filepath.SkipAll
		}

		// Process file and search for matches
		fileMatches := searchFileForPattern(path, re, maxMatches-len(matches))
		matches = append(matches, fileMatches...)

		return nil
	})

	if err != nil {
		return nil, grepFilesOutput{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return nil, grepFilesOutput{
		Matches: matches,
		Count:   len(matches),
		Success: true,
	}, nil
}

// StartFileSystemMCPServer starts the filesystem MCP server
func StartFileSystemMCPServer(ctx context.Context, transport mcp.Transport) error {
	// Create MCP server for filesystem operations
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "filesystem",
		Version: "v1.0.0",
	}, nil)

	// Per-server state gating edits behind a prior read or write of the file.
	fs := newFileSystem()

	// Add tool for reading files
	mcp.AddTool(server, &mcp.Tool{
		Name:        "read",
		Description: "Read file with line numbers, supports optional offset and limit for reading specific line ranges",
	}, fs.read)

	// Add tool for writing files
	mcp.AddTool(server, &mcp.Tool{
		Name:        "write",
		Description: "Write content to a file, creates parent directories if needed, overwrites existing files",
	}, fs.write)

	// Add tool for editing files
	mcp.AddTool(server, &mcp.Tool{
		Name:        "edit",
		Description: "Replace old string with new string in a file. The file must be read (or written) first; old string must be unique unless all=true",
	}, fs.edit)

	// Add tool for glob file matching
	mcp.AddTool(server, &mcp.Tool{
		Name:        "glob",
		Description: "Find files by glob pattern, sorted by modification time (newest first)",
	}, globFiles)

	// Add tool for grep file search
	mcp.AddTool(server, &mcp.Tool{
		Name:        "grep",
		Description: "Search files for regex pattern, returns up to 50 matches",
	}, grepFiles)

	// Run the server
	if err := server.Run(ctx, transport); err != nil {
		return err
	}

	return nil
}
