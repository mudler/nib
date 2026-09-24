package mcp

import (
	"fmt"
	"strconv"
	"strings"
)

// formatArtifactURI renders an artifact ID as "artifact://N".
func formatArtifactURI(id int64) string {
	return fmt.Sprintf("artifact://%d", id)
}

// ParseArtifactURI extracts the numeric ID from an "artifact://N" URI.
// Returns ok=false if the string is not an artifact URI.
func ParseArtifactURI(s string) (id int64, ok bool) {
	const prefix = "artifact://"
	if !strings.HasPrefix(s, prefix) {
		return 0, false
	}
	n, err := strconv.ParseInt(s[len(prefix):], 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
