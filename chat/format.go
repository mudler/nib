package chat

import (
	"bytes"
	"encoding/json"
)

// PrettyJSON indents a JSON string for display. If s is not valid JSON it is
// returned unchanged.
func PrettyJSON(s string) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(s), "", "  "); err != nil {
		return s
	}
	return buf.String()
}
