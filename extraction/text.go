package extraction

import (
	"os"
	"path/filepath"
	"strings"
)

var plainTextExts = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".csv": true, ".json": true,
	".yaml": true, ".yml": true, ".xml": true, ".html": true, ".htm": true,
	".rtf": true, ".log": true, ".toml": true, ".ini": true, ".rst": true,
	".tex": true, ".cfg": true, ".conf": true,
}

// IsPlainTextFile reports whether path has a known plain-text extension.
func IsPlainTextFile(path string) bool {
	return plainTextExts[strings.ToLower(filepath.Ext(path))]
}

// ReadTextFile reads a plain-text file and returns its content coerced to valid
// UTF-8. HTML/RTF are returned verbatim (no tag stripping) — a documented v1 limit.
func ReadTextFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.ToValidUTF8(string(b), ""), nil
}
