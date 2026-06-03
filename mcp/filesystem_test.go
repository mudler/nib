package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestReadFile tests the read file functionality
func TestReadFile(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		input       readFileInput
		wantSuccess bool
		wantError   bool
		checkFunc   func(t *testing.T, output readFileOutput)
	}{
		{
			name:    "read entire file successfully",
			content: "line 1\nline 2\nline 3",
			input: readFileInput{
				Path: "", // will be set in test
			},
			wantSuccess: true,
			checkFunc: func(t *testing.T, output readFileOutput) {
				if output.TotalLines != 3 {
					t.Errorf("expected 3 lines, got %d", output.TotalLines)
				}
				// Check that line numbers are present and formatted correctly
				lines := strings.Split(output.Content, "\n")
				if len(lines) != 3 {
					t.Errorf("expected 3 lines in content, got %d", len(lines))
				}
				// Verify line 1 contains "line 1" with line number
				if !strings.Contains(lines[0], "1|") || !strings.Contains(lines[0], "line 1") {
					t.Errorf("expected line 1 with number, got: %s", lines[0])
				}
				// Verify line 2 contains "line 2" with line number
				if !strings.Contains(lines[1], "2|") || !strings.Contains(lines[1], "line 2") {
					t.Errorf("expected line 2 with number, got: %s", lines[1])
				}
			},
		},
		{
			name:    "read with offset and limit",
			content: "line 1\nline 2\nline 3\nline 4\nline 5",
			input: readFileInput{
				Path:   "", // will be set in test
				Offset: 1,
				Limit:  2,
			},
			wantSuccess: true,
			checkFunc: func(t *testing.T, output readFileOutput) {
				if output.TotalLines != 5 {
					t.Errorf("expected 5 lines, got %d", output.TotalLines)
				}
				// Verify content starts at line 2 and includes line 3
				if !strings.Contains(output.Content, "2|") || !strings.Contains(output.Content, "line 2") {
					t.Errorf("expected to start at line 2")
				}
				if !strings.Contains(output.Content, "3|") || !strings.Contains(output.Content, "line 3") {
					t.Errorf("expected to include line 3")
				}
				if strings.Contains(output.Content, "line 4") {
					t.Errorf("should not include line 4 with limit 2")
				}
			},
		},
		{
			name:    "read with offset beyond file length",
			content: "line 1\nline 2",
			input: readFileInput{
				Path:   "", // will be set in test
				Offset: 10,
			},
			wantSuccess: true,
			checkFunc: func(t *testing.T, output readFileOutput) {
				if output.TotalLines != 2 {
					t.Errorf("expected 2 lines, got %d", output.TotalLines)
				}
				if output.Content != "" {
					t.Errorf("expected empty content, got: %s", output.Content)
				}
			},
		},
		{
			name:    "read non-existent file",
			content: "",
			input: readFileInput{
				Path: "/nonexistent/file.txt",
			},
			wantSuccess: false,
			wantError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp file if content is provided
			if tt.content != "" {
				tmpDir := t.TempDir()
				tmpFile := filepath.Join(tmpDir, "test.txt")
				if err := os.WriteFile(tmpFile, []byte(tt.content), 0644); err != nil {
					t.Fatal(err)
				}
				tt.input.Path = tmpFile
			}

			// Call readFile
			_, output, err := readFile(context.Background(), &mcp.CallToolRequest{}, tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if output.Success != tt.wantSuccess {
				t.Errorf("expected success=%v, got %v (error: %s)", tt.wantSuccess, output.Success, output.Error)
			}

			if tt.wantError && output.Error == "" {
				t.Error("expected error message but got none")
			}

			if tt.checkFunc != nil && output.Success {
				tt.checkFunc(t, output)
			}
		})
	}
}

// TestWriteFile tests the write file functionality
func TestWriteFile(t *testing.T) {
	tests := []struct {
		name        string
		input       writeFileInput
		wantSuccess bool
		checkFunc   func(t *testing.T, path string)
	}{
		{
			name: "write to new file",
			input: writeFileInput{
				Path:    "", // will be set in test
				Content: "test content",
			},
			wantSuccess: true,
			checkFunc: func(t *testing.T, path string) {
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("failed to read file: %v", err)
				}
				if string(content) != "test content" {
					t.Errorf("expected 'test content', got '%s'", string(content))
				}
			},
		},
		{
			name: "overwrite existing file",
			input: writeFileInput{
				Path:    "", // will be set in test
				Content: "new content",
			},
			wantSuccess: true,
			checkFunc: func(t *testing.T, path string) {
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("failed to read file: %v", err)
				}
				if string(content) != "new content" {
					t.Errorf("expected 'new content', got '%s'", string(content))
				}
			},
		},
		{
			name: "create parent directories automatically",
			input: writeFileInput{
				Path:    "", // will be set in test with nested path
				Content: "nested content",
			},
			wantSuccess: true,
			checkFunc: func(t *testing.T, path string) {
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("failed to read file: %v", err)
				}
				if string(content) != "nested content" {
					t.Errorf("expected 'nested content', got '%s'", string(content))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			var filePath string

			if tt.name == "create parent directories automatically" {
				filePath = filepath.Join(tmpDir, "subdir1", "subdir2", "test.txt")
			} else {
				filePath = filepath.Join(tmpDir, "test.txt")
			}

			// For overwrite test, create file first
			if tt.name == "overwrite existing file" {
				if err := os.WriteFile(filePath, []byte("old content"), 0644); err != nil {
					t.Fatal(err)
				}
			}

			tt.input.Path = filePath

			// Call writeFile
			_, output, err := writeFile(context.Background(), &mcp.CallToolRequest{}, tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if output.Success != tt.wantSuccess {
				t.Errorf("expected success=%v, got %v (error: %s)", tt.wantSuccess, output.Success, output.Error)
			}

			if tt.checkFunc != nil && output.Success {
				tt.checkFunc(t, filePath)
			}
		})
	}
}

// TestEditFile tests the edit file functionality
func TestEditFile(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		input       editFileInput
		wantSuccess bool
		wantRepl    int
		checkFunc   func(t *testing.T, path string, output editFileOutput)
	}{
		{
			name:    "replace unique string successfully",
			content: "hello world",
			input: editFileInput{
				Path: "", // will be set in test
				Old:  "world",
				New:  "universe",
			},
			wantSuccess: true,
			wantRepl:    1,
			checkFunc: func(t *testing.T, path string, output editFileOutput) {
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("failed to read file: %v", err)
				}
				if string(content) != "hello universe" {
					t.Errorf("expected 'hello universe', got '%s'", string(content))
				}
			},
		},
		{
			name:    "replace all occurrences with all=true",
			content: "foo bar foo baz foo",
			input: editFileInput{
				Path: "", // will be set in test
				Old:  "foo",
				New:  "qux",
				All:  true,
			},
			wantSuccess: true,
			wantRepl:    3,
			checkFunc: func(t *testing.T, path string, output editFileOutput) {
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("failed to read file: %v", err)
				}
				if string(content) != "qux bar qux baz qux" {
					t.Errorf("expected 'qux bar qux baz qux', got '%s'", string(content))
				}
				if output.Replacements != 3 {
					t.Errorf("expected 3 replacements, got %d", output.Replacements)
				}
			},
		},
		{
			name:    "error when old string not found",
			content: "hello world",
			input: editFileInput{
				Path: "", // will be set in test
				Old:  "notfound",
				New:  "something",
			},
			wantSuccess: false,
		},
		{
			name:    "error when old string appears multiple times and all=false",
			content: "foo bar foo baz foo",
			input: editFileInput{
				Path: "", // will be set in test
				Old:  "foo",
				New:  "qux",
				All:  false,
			},
			wantSuccess: false,
			checkFunc: func(t *testing.T, path string, output editFileOutput) {
				if !strings.Contains(output.Error, "appears 3 times") {
					t.Errorf("expected error about multiple occurrences, got: %s", output.Error)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "test.txt")
			if err := os.WriteFile(tmpFile, []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}
			tt.input.Path = tmpFile

			// Call editFile
			_, output, err := editFile(context.Background(), &mcp.CallToolRequest{}, tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if output.Success != tt.wantSuccess {
				t.Errorf("expected success=%v, got %v (error: %s)", tt.wantSuccess, output.Success, output.Error)
			}

			if tt.wantSuccess && output.Replacements != tt.wantRepl {
				t.Errorf("expected %d replacements, got %d", tt.wantRepl, output.Replacements)
			}

			if tt.checkFunc != nil {
				tt.checkFunc(t, tmpFile, output)
			}
		})
	}
}

// TestEditRequiresRead verifies that edits are gated on a prior read of the
// same file: editing a file that was never read must fail, and editing it
// after a successful read must succeed.
func TestEditRequiresRead(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	fs := newFileSystem()
	ctx := context.Background()

	// Editing before reading must be rejected and must not modify the file.
	_, out, err := fs.edit(ctx, &mcp.CallToolRequest{}, editFileInput{
		Path: tmpFile,
		Old:  "world",
		New:  "universe",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Success {
		t.Fatal("expected edit to fail before the file was read")
	}
	if !strings.Contains(out.Error, "read") {
		t.Errorf("expected error to mention reading the file first, got: %s", out.Error)
	}
	content, _ := os.ReadFile(tmpFile)
	if string(content) != "hello world" {
		t.Errorf("file should be unchanged, got: %s", string(content))
	}

	// Read the file, then the same edit must succeed.
	if _, rout, err := fs.read(ctx, &mcp.CallToolRequest{}, readFileInput{Path: tmpFile}); err != nil || !rout.Success {
		t.Fatalf("read failed: err=%v success=%v", err, rout.Success)
	}
	_, out, err = fs.edit(ctx, &mcp.CallToolRequest{}, editFileInput{
		Path: tmpFile,
		Old:  "world",
		New:  "universe",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.Success {
		t.Fatalf("expected edit to succeed after reading, got error: %s", out.Error)
	}
	content, _ = os.ReadFile(tmpFile)
	if string(content) != "hello universe" {
		t.Errorf("expected 'hello universe', got: %s", string(content))
	}
}

// TestEditAfterWrite verifies that writing a file also unlocks editing it,
// since the agent already knows the contents it just wrote.
func TestEditAfterWrite(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "new.txt")

	fs := newFileSystem()
	ctx := context.Background()

	if _, wout, err := fs.write(ctx, &mcp.CallToolRequest{}, writeFileInput{Path: tmpFile, Content: "foo bar"}); err != nil || !wout.Success {
		t.Fatalf("write failed: err=%v success=%v", err, wout.Success)
	}
	_, out, err := fs.edit(ctx, &mcp.CallToolRequest{}, editFileInput{Path: tmpFile, Old: "bar", New: "baz"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.Success {
		t.Fatalf("expected edit to succeed after writing, got error: %s", out.Error)
	}
}

// TestEditReadPathNormalization verifies that the gate matches paths
// regardless of how they are expressed (relative vs. cleaned).
func TestEditReadPathNormalization(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	fs := newFileSystem()
	ctx := context.Background()

	// Read via a path containing a redundant "." segment.
	readPath := filepath.Join(tmpDir, ".", "test.txt")
	if _, rout, err := fs.read(ctx, &mcp.CallToolRequest{}, readFileInput{Path: readPath}); err != nil || !rout.Success {
		t.Fatalf("read failed: err=%v success=%v", err, rout.Success)
	}

	// Edit via the cleaned path: should be recognized as the same file.
	_, out, err := fs.edit(ctx, &mcp.CallToolRequest{}, editFileInput{Path: tmpFile, Old: "world", New: "universe"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.Success {
		t.Fatalf("expected edit to succeed across equivalent paths, got error: %s", out.Error)
	}
}

// TestGlobFiles tests the glob file functionality
func TestGlobFiles(t *testing.T) {
	tests := []struct {
		name        string
		setupFiles  []string
		input       globFilesInput
		wantSuccess bool
		checkFunc   func(t *testing.T, output globFilesOutput)
	}{
		{
			name:       "match files with simple pattern",
			setupFiles: []string{"test1.go", "test2.go", "test.txt"},
			input: globFilesInput{
				Pat:  "*.go",
				Path: "", // will be set in test
			},
			wantSuccess: true,
			checkFunc: func(t *testing.T, output globFilesOutput) {
				if output.Count != 2 {
					t.Errorf("expected 2 files, got %d", output.Count)
				}
			},
		},
		{
			name:       "match files with recursive pattern",
			setupFiles: []string{"test.go", "subdir/test2.go", "subdir/nested/test3.go"},
			input: globFilesInput{
				Pat:  "**/*.go",
				Path: "", // will be set in test
			},
			wantSuccess: true,
			checkFunc: func(t *testing.T, output globFilesOutput) {
				if output.Count < 2 {
					t.Errorf("expected at least 2 files, got %d", output.Count)
				}
			},
		},
		{
			name:       "empty result for non-matching pattern",
			setupFiles: []string{"test1.go", "test2.go"},
			input: globFilesInput{
				Pat:  "*.txt",
				Path: "", // will be set in test
			},
			wantSuccess: true,
			checkFunc: func(t *testing.T, output globFilesOutput) {
				if output.Count != 0 {
					t.Errorf("expected 0 files, got %d", output.Count)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			// Create test files with different modification times
			for i, file := range tt.setupFiles {
				fullPath := filepath.Join(tmpDir, file)
				dir := filepath.Dir(fullPath)
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(fullPath, []byte("content"), 0644); err != nil {
					t.Fatal(err)
				}

				// Set different modification times to test sorting
				modTime := time.Now().Add(time.Duration(-i) * time.Second)
				if err := os.Chtimes(fullPath, modTime, modTime); err != nil {
					t.Fatal(err)
				}
			}

			tt.input.Path = tmpDir

			// Call globFiles
			_, output, err := globFiles(context.Background(), &mcp.CallToolRequest{}, tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if output.Success != tt.wantSuccess {
				t.Errorf("expected success=%v, got %v (error: %s)", tt.wantSuccess, output.Success, output.Error)
			}

			if tt.checkFunc != nil {
				tt.checkFunc(t, output)
			}

			// Verify files are sorted by modification time (newest first)
			if len(output.Files) > 1 {
				for i := 0; i < len(output.Files)-1; i++ {
					info1, _ := os.Stat(output.Files[i])
					info2, _ := os.Stat(output.Files[i+1])
					if info1.ModTime().Before(info2.ModTime()) {
						t.Errorf("files not sorted by modification time (newest first)")
						break
					}
				}
			}
		})
	}
}

// TestGrepFiles tests the grep file functionality
func TestGrepFiles(t *testing.T) {
	tests := []struct {
		name        string
		setupFiles  map[string]string
		input       grepFilesInput
		wantSuccess bool
		checkFunc   func(t *testing.T, output grepFilesOutput)
	}{
		{
			name: "find matches with simple regex",
			setupFiles: map[string]string{
				"test1.txt": "hello world\nfoo bar\nhello again",
				"test2.txt": "no match here",
			},
			input: grepFilesInput{
				Pat:  "hello",
				Path: "", // will be set in test
			},
			wantSuccess: true,
			checkFunc: func(t *testing.T, output grepFilesOutput) {
				if output.Count != 2 {
					t.Errorf("expected 2 matches, got %d", output.Count)
				}
				for _, match := range output.Matches {
					if !strings.Contains(match, "hello") {
						t.Errorf("match doesn't contain 'hello': %s", match)
					}
					// Verify format: filepath:linenum:content
					parts := strings.SplitN(match, ":", 3)
					if len(parts) != 3 {
						t.Errorf("invalid match format, expected filepath:linenum:content, got: %s", match)
					}
				}
			},
		},
		{
			name: "find matches with complex regex",
			setupFiles: map[string]string{
				"test.txt": "test123\ntest456\nnothing\ntest789",
			},
			input: grepFilesInput{
				Pat:  "test\\d+",
				Path: "", // will be set in test
			},
			wantSuccess: true,
			checkFunc: func(t *testing.T, output grepFilesOutput) {
				if output.Count != 3 {
					t.Errorf("expected 3 matches, got %d", output.Count)
				}
			},
		},
		{
			name: "invalid regex pattern",
			setupFiles: map[string]string{
				"test.txt": "content",
			},
			input: grepFilesInput{
				Pat:  "[invalid((",
				Path: "", // will be set in test
			},
			wantSuccess: false,
			checkFunc: func(t *testing.T, output grepFilesOutput) {
				if !strings.Contains(output.Error, "invalid regex pattern") {
					t.Errorf("expected invalid regex error, got: %s", output.Error)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			// Create test files
			for file, content := range tt.setupFiles {
				fullPath := filepath.Join(tmpDir, file)
				if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}

			tt.input.Path = tmpDir

			// Call grepFiles
			_, output, err := grepFiles(context.Background(), &mcp.CallToolRequest{}, tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if output.Success != tt.wantSuccess {
				t.Errorf("expected success=%v, got %v (error: %s)", tt.wantSuccess, output.Success, output.Error)
			}

			if tt.checkFunc != nil {
				tt.checkFunc(t, output)
			}
		})
	}
}

// TestGrepFilesLimit tests the 50 match limit
func TestGrepFilesLimit(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file with more than 50 matching lines
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "match line")
	}
	content := strings.Join(lines, "\n")
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	input := grepFilesInput{
		Pat:  "match",
		Path: tmpDir,
	}

	_, output, err := grepFiles(context.Background(), &mcp.CallToolRequest{}, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !output.Success {
		t.Errorf("expected success, got error: %s", output.Error)
	}

	if output.Count != 50 {
		t.Errorf("expected 50 matches (limit), got %d", output.Count)
	}
}

// TestStartFileSystemMCPServer tests the server creation
func TestStartFileSystemMCPServer(t *testing.T) {
	// Create in-memory transports
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	// Start server in goroutine
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErrChan := make(chan error, 1)
	go func() {
		err := StartFileSystemMCPServer(ctx, serverTransport)
		serverErrChan <- err
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Test that we can create a client and connect
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "v1.0.0",
	}, nil)

	// Connect client (this will test that server is running)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("failed to connect to server: %v", err)
	}

	// List tools to verify they're registered
	result, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("failed to list tools: %v", err)
	}

	expectedTools := []string{"read", "write", "edit", "glob", "grep"}

	for _, expectedTool := range expectedTools {
		found := false
		for _, tool := range result.Tools {
			if tool.Name == expectedTool {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected tool '%s' not found in server tools", expectedTool)
		}
	}

	// Cancel context to stop server
	cancel()

	// Wait for server to stop
	select {
	case <-serverErrChan:
		// Server stopped, expected
	case <-time.After(time.Second):
		t.Error("server did not stop after context cancellation")
	}
}
