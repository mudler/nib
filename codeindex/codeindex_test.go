package codeindex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleGo = `package main

import (
	"fmt"
	"os"

	"github.com/mudler/cogito"
)

type Config struct {
	root   string
	mu     sync.Mutex
	seen   map[string]bool
	count  int
	logger *slog.Logger
}

type Handler interface {
	Handle(req *Request) error
	Close() error
}

type MyAlias = string

const (
	MaxRetries     = 3
	DefaultTimeout = 30
)

var ErrNotFound = errors.New("not found")

func main() {
	fmt.Println("hello")
}

func process(data []byte) error {
	return nil
}

func (c *Config) Validate() bool {
	return true
}
`

func TestIndexGoFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(path, []byte(sampleGo), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := Index(path)
	if err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")

	// Package
	if !contains(lines, "Package: main [1]") {
		t.Fatalf("missing package entry in:\n%s", out)
	}

	// Imports
	if !contains(lines, `Import: "fmt", "os", "github.com/mudler/cogito" [3-8]`) {
		t.Fatalf("missing or wrong import entry in:\n%s", out)
	}

	// Type: Config struct with fields
	if !contains(lines, "Type: Config struct [10-16]") {
		t.Fatalf("missing Config struct type in:\n%s", out)
	}
	if !contains(lines, "  root string") {
		t.Fatalf("missing struct field 'root string' in:\n%s", out)
	}
	if !contains(lines, "  mu sync.Mutex") {
		t.Fatalf("missing struct field 'mu sync.Mutex' in:\n%s", out)
	}

	// Type: Handler interface with methods
	if !contains(lines, "Type: Handler interface [18-21]") {
		t.Fatalf("missing Handler interface type in:\n%s", out)
	}
	if !contains(lines, "  Handle(req *Request) error") {
		t.Fatalf("missing interface method 'Handle' in:\n%s", out)
	}
	if !contains(lines, "  Close() error") {
		t.Fatalf("missing interface method 'Close' in:\n%s", out)
	}

	// Type alias
	if !contains(lines, "Type: MyAlias = string [23]") {
		t.Fatalf("missing type alias MyAlias in:\n%s", out)
	}

	// Constants
	if !strings.Contains(out, "Constant: MaxRetries = 3") {
		t.Fatalf("missing MaxRetries constant in:\n%s", out)
	}

	// Variable
	if !strings.Contains(out, "Variable: ErrNotFound = errors.New(\"not found\")") {
		t.Fatalf("missing ErrNotFound variable in:\n%s", out)
	}

	// Function
	if !contains(lines, "Function: main() [32-34]") {
		t.Fatalf("missing main function in:\n%s", out)
	}
	if !contains(lines, "Function: process(data []byte) error [36-38]") {
		t.Fatalf("missing process function in:\n%s", out)
	}

	// Method
	if !contains(lines, "Method: (c *Config) Validate() bool [40-42]") {
		t.Fatalf("missing Validate method in:\n%s", out)
	}
}

func TestIndexUnsupportedExt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Index(path)
	if err == nil {
		t.Fatal("expected error for unsupported file type")
	}
	if !strings.Contains(err.Error(), "no extractor") {
		t.Fatalf("expected 'no extractor' error, got: %v", err)
	}
}

func TestIndexFileTooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.go")
	// Create a file just over 2 MB
	data := make([]byte, (2<<20)+1)
	for i := range data {
		data[i] = 'x'
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Index(path)
	if err == nil {
		t.Fatal("expected error for oversized file")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected 'exceeds' error, got: %v", err)
	}
}

func TestIndexEmptyGoFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.go")
	if err := os.WriteFile(path, []byte("package empty\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := Index(path)
	if err != nil {
		t.Fatalf("Index failed: %v", err)
	}
	if !strings.Contains(out, "Package: empty") {
		t.Fatalf("expected package entry in output:\n%s", out)
	}
}

func TestIndexGoFileWithSyntaxErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.go")
	src := `package main

func good() {
}

func broken( {
}
`
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := Index(path)
	if err != nil {
		t.Fatalf("Index should not fail on syntax errors: %v", err)
	}
	if !strings.Contains(out, "Package: main") {
		t.Fatalf("expected package entry despite syntax errors:\n%s", out)
	}
	if !strings.Contains(out, "Function: good()") {
		t.Fatalf("expected good() function despite syntax errors:\n%s", out)
	}
}

func TestSupportedExtensions(t *testing.T) {
	exts := SupportedExtensions()
	found := false
	for _, e := range exts {
		if e == ".go" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected .go in supported extensions: %v", exts)
	}
}

func TestLineRange(t *testing.T) {
	if got := lineRange(5, 5); got != "5" {
		t.Fatalf("lineRange(5,5) = %q, want %q", got, "5")
	}
	if got := lineRange(5, 10); got != "5-10" {
		t.Fatalf("lineRange(5,10) = %q, want %q", got, "5-10")
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("truncate(\"short\", 10) = %q, want %q", got, "short")
	}
	long := strings.Repeat("a", 20)
	got := truncate(long, 10)
	if len(got) != 10 {
		t.Fatalf("truncate result len = %d, want 10", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("truncate should end with ..., got %q", got)
	}
}

func TestFormatEntriesFieldTruncation(t *testing.T) {
	fields := make([]string, 20)
	for i := range fields {
		fields[i] = "field" + string(rune('a'+i))
	}
	entries := []Entry{
		{
			Section:   SectionType,
			Name:      "Big",
			Detail:    "Big struct",
			StartLine: 1,
			EndLine:   30,
			Fields:    fields,
		},
	}
	out := formatEntries(entries, false)
	if !strings.Contains(out, "... (12 more)") {
		t.Fatalf("expected field truncation with '... (12 more)' in:\n%s", out)
	}
}

func contains(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

func TestEntriesReturnsRawEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(path, []byte(sampleGo), 0644); err != nil {
		t.Fatal(err)
	}

	entries, _, err := Entries(path)
	if err != nil {
		t.Fatalf("Entries failed: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("Entries returned no entries")
	}

	// Verify the return type is []Entry (not a formatted string).
	// Some entries (imports, constants, vars) may have an empty
	// Name (the detail is in the Detail field), so skip those when
	// checking for non-empty names.
	for _, e := range entries {
		switch e.Section {
		case SectionImport, SectionConst, SectionVar, SectionHeading, SectionPackage:
			continue
		}
		if e.Name == "" {
			t.Fatalf("entry has empty name: %+v", e)
		}
	}

	// Spot-check that we got at least one Function entry, since sampleGo defines
	// main, process, and the Validate method.
	var hasFunc bool
	for _, e := range entries {
		if e.Section == SectionFunc {
			hasFunc = true
			break
		}
	}
	if !hasFunc {
		t.Fatalf("expected at least one Function entry, got: %+v", entries)
	}
}
