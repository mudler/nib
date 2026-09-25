package mcp

import (
	"bufio"
	"bytes"
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
	"github.com/mudler/nib/codeindex"
)

// fileSystem holds per-server state that gates mutating operations. An edit is
// only allowed once the agent has seen the file's contents, either by reading
// it or by writing it. This prevents blind edits against files the agent has
// never inspected.
type fileSystem struct {
	// root, when non-empty, is the working directory that relative paths are
	// rooted at. Empty means paths are used verbatim (legacy process-cwd
	// behavior).
	root string
	mu   sync.Mutex
	seen map[string]bool

	// limits and artifacts are set by StartFileSystemMCPServer so the read
	// tool can apply the same output-limiting pipeline as bash.
	limits    *OutputLimitsPolicy
	artifacts *ArtifactStore
}

// newFileSystem creates a filesystem handler with an empty seen-file set,
// rooted at root. An empty root preserves the legacy process-cwd behavior.
func newFileSystem(root string) *fileSystem {
	return &fileSystem{root: root, seen: make(map[string]bool)}
}

// resolve roots a relative path at the configured working dir. Absolute paths
// and the empty-root case are returned unchanged.
func (f *fileSystem) resolve(path string) string {
	if f.root == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(f.root, path)
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
	// artifact://N paths serve spilled tool output from the in-memory store.
	if id, ok := ParseArtifactURI(input.Path); ok {
		return f.readArtifact(ctx, req, input, id)
	}

	input.Path = f.resolve(input.Path)
	res, out, err := readFile(ctx, req, input)
	// An outline is not the file's contents: edit matches exact text, so it
	// stays locked until the model reads the lines it wants to change.
	if out.Success && !out.Outline {
		f.markSeen(input.Path)
	}
	// Apply output limits to non-outline reads. Outlines are already
	// compact; limiting them would only mangle the structure.
	if out.Success && !out.Outline && f.limits != nil {
		out.Content = LimitOutput(out.Content, "read", f.limits.Resolved(), f.artifacts)
		res = nil // rebuild result below
	}
	return res, out, err
}

// readArtifact serves an artifact from the store, with the same offset/limit
// paging as a file read. The content is line-numbered just like a file.
func (f *fileSystem) readArtifact(ctx context.Context, req *mcp.CallToolRequest, input readFileInput, id int64) (
	*mcp.CallToolResult,
	readFileOutput,
	error,
) {
	if f.artifacts == nil {
		return nil, readFileOutput{
			Success: false,
			Error:   "artifact store is not available",
		}, nil
	}
	a := f.artifacts.Get(id)
	if a == nil {
		return nil, readFileOutput{
			Success: false,
			Error:   fmt.Sprintf("artifact %d not found", id),
		}, nil
	}

	lines := strings.Split(a.Content, "\n")
	totalLines := len(lines)

	offset := input.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= totalLines {
		return nil, readFileOutput{
			Content:    "",
			TotalLines: totalLines,
			Success:    true,
		}, nil
	}

	endIndex := totalLines
	if input.Limit > 0 {
		endIndex = offset + input.Limit
		if endIndex > totalLines {
			endIndex = totalLines
		}
	}

	width := len(fmt.Sprintf("%d", totalLines))
	if width < 4 {
		width = 4
	}

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

// write writes a file and records it as seen: after writing, the agent knows
// the file's exact contents, so a subsequent edit is no longer blind.
func (f *fileSystem) write(ctx context.Context, req *mcp.CallToolRequest, input writeFileInput) (
	*mcp.CallToolResult,
	writeFileOutput,
	error,
) {
	input.Path = f.resolve(input.Path)
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
	input.Path = f.resolve(input.Path)
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
	Outline    bool   `json:"outline,omitempty" jsonschema:"true when the file was too large to return whole and content is its outline; read line ranges with offset and limit"`
	Success    bool   `json:"success" jsonschema:"whether operation was successful"`
	Error      string `json:"error,omitempty" jsonschema:"error message if failed"`
}

// outlineMinLines is the size above which a read with no range returns the
// file's outline instead of its lines, when codeindex supports the file type.
// A whole read of a file this long costs tens of thousands of tokens, and the
// model usually wants one function of it. Explicit offset/limit always return
// lines.
const outlineMinLines = 1000

// outlineRead returns the outline read result for a large source file, or false
// when codeindex cannot index it and the read must return lines.
func outlineRead(path string, totalLines int) (readFileOutput, bool) {
	outline, err := codeindex.Index(path)
	if err != nil || strings.TrimSpace(outline) == "" {
		return readFileOutput{}, false
	}
	header := fmt.Sprintf("[%d lines, too large to return whole. This is the file's outline, with line ranges in []. "+
		"Read the part you need with offset and limit.]\n\n", totalLines)
	return readFileOutput{
		Content:    header + outline,
		TotalLines: totalLines,
		Outline:    true,
		Success:    true,
	}, true
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

// glob roots the base path at the working dir before delegating to globFiles.
// resolve("") returns "" when root is empty (globFiles then defaults to "."),
// and returns the root itself when a root is configured.
func (f *fileSystem) glob(ctx context.Context, req *mcp.CallToolRequest, input globFilesInput) (
	*mcp.CallToolResult,
	globFilesOutput,
	error,
) {
	input.Path = f.resolve(input.Path)
	return globFiles(ctx, req, input)
}

// grep roots the base path at the working dir before delegating to grepFiles.
func (f *fileSystem) grep(ctx context.Context, req *mcp.CallToolRequest, input grepFilesInput) (
	*mcp.CallToolResult,
	grepFilesOutput,
	error,
) {
	input.Path = f.resolve(input.Path)
	return grepFiles(ctx, req, input)
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

	if input.Offset <= 0 && input.Limit <= 0 && totalLines > outlineMinLines {
		if out, ok := outlineRead(input.Path, totalLines); ok {
			return nil, out, nil
		}
	}

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

	matches := []string{}

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
				Files:   []string{},
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
				Files:   []string{},
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

// binarySniffLen is how much of a file grep reads to tell text from binary,
// the same heuristic git and ripgrep use: a NUL byte in the head.
const binarySniffLen = 8000

// searchFileForPattern searches a text file for regex pattern matches. A
// binary file (weights, fixtures, objects) has no lines worth matching and
// can be gigabytes, so it is skipped.
func searchFileForPattern(path string, re *regexp.Regexp, maxMatches int) []string {
	matches := []string{}

	file, err := os.Open(path)
	if err != nil {
		return matches // Skip files that can't be opened
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	if head, _ := reader.Peek(binarySniffLen); bytes.IndexByte(head, 0) >= 0 {
		return matches
	}

	scanner := bufio.NewScanner(reader)
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

// grepSkipDirs are directories grep does not enter: VCS metadata and
// dependency trees, which are large and never what the model is looking for.
var grepSkipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true,
}

// grepCandidates calls visit for each file grep searches under basePath and
// stops when visit returns false or ctx is done. It walks the whole tree,
// ignored and untracked files included, but not grepSkipDirs.
func grepCandidates(ctx context.Context, basePath string, visit func(path string) bool) error {
	info, err := os.Stat(basePath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		visit(basePath)
		return ctx.Err()
	}

	return filepath.WalkDir(basePath, func(path string, d os.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return nil // Skip errors
		}
		if d.IsDir() {
			if path != basePath && grepSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if !visit(path) {
			return filepath.SkipAll
		}
		return nil
	})
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
			Matches: []string{},
			Success: false,
			Error:   fmt.Sprintf("invalid regex pattern: %s", err.Error()),
		}, nil
	}

	basePath := input.Path
	if basePath == "" {
		basePath = "."
	}

	matches := []string{}
	const maxMatches = 50

	err = grepCandidates(ctx, basePath, func(path string) bool {
		matches = append(matches, searchFileForPattern(path, re, maxMatches-len(matches))...)
		return len(matches) < maxMatches
	})

	if err != nil {
		return nil, grepFilesOutput{
			Matches: []string{},
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

// StartFileSystemMCPServer starts the filesystem MCP server. When root is
// non-empty, relative paths are rooted at it; an empty root preserves the
// legacy process-cwd behavior.
func StartFileSystemMCPServer(ctx context.Context, transport mcp.Transport, root string, limits *OutputLimitsPolicy, artifacts *ArtifactStore) error {
	// Create MCP server for filesystem operations
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "filesystem",
		Version: "v1.0.0",
	}, nil)

	// Per-server state gating edits behind a prior read or write of the file.
	fs := newFileSystem(root)
	fs.limits = limits
	fs.artifacts = artifacts

	// Add tool for reading files
	mcp.AddTool(server, &mcp.Tool{
		Name: "read",
		Description: fmt.Sprintf("Read file with line numbers, supports optional offset and limit for reading specific line ranges. "+
			"A source file over %d lines read without offset or limit returns its outline instead (outline=true); "+
			"read the lines you need from it with offset and limit.", outlineMinLines),
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
	}, fs.glob)

	// Add tool for grep file search
	mcp.AddTool(server, &mcp.Tool{
		Name:        "grep",
		Description: "Search files for regex pattern, returns up to 50 matches. Skips binary files and .git, .hg, .svn and node_modules directories",
	}, fs.grep)

	// Add tool for searching compaction artifacts
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_artifacts",
		Description: "Search artifacts saved during compaction. Accepts a regex pattern (case-insensitive). Returns matching lines with 3 lines of context before and after, artifact IDs, and line numbers. Use read with an artifact://N path to page through full content.",
	}, fs.searchArtifacts)

	// Run the server
	if err := server.Run(ctx, transport); err != nil {
		return err
	}

	return nil
}

type searchArtifactsInput struct {
	Pattern string `json:"pattern" jsonschema:"regular expression pattern (case-insensitive)"`
}

type searchArtifactsOutput struct {
	Success bool   `json:"success" jsonschema:"whether operation was successful"`
	Error   string `json:"error,omitempty" jsonschema:"error message if failed"`
}

func (f *fileSystem) searchArtifacts(ctx context.Context, req *mcp.CallToolRequest, input searchArtifactsInput) (
	*mcp.CallToolResult,
	searchArtifactsOutput,
	error,
) {
	if f.artifacts == nil || f.artifacts.Count() == 0 {
		return textResult("No artifacts available to search."), searchArtifactsOutput{Success: true}, nil
	}
	results, err := f.artifacts.Search(input.Pattern)
	if err != nil {
		return textResult(fmt.Sprintf("Invalid regex pattern: %v", err)), searchArtifactsOutput{Success: false, Error: "invalid regex pattern"}, nil
	}
	if len(results) == 0 {
		return textResult("No matches found."), searchArtifactsOutput{Success: true}, nil
	}
	var b strings.Builder
	for _, r := range results {
		fmt.Fprintf(&b, "%s, line %d:\n%s\n", formatArtifactURI(r.ID), r.Line, r.Text)
	}
	return textResult(b.String()), searchArtifactsOutput{Success: true}, nil
}
