package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// usageFileName is the session spend report written beside the transcript.
const usageFileName = "usage.json"

// WriteUsage writes v to <dir>/usage.json, replacing any previous report.
//
// A separate file rather than a final trace.ndjson record: Record's schema
// mirrors voro/claudemaster's trace.Call so the transcript stays directly
// consumable by that pipeline, and a summary record with no request would
// break it. A standalone JSON object is also simpler for a benchmark harness
// to read than the last line of an NDJSON stream.
//
// Replacing rather than appending is what makes a second call safe: the session
// Close that calls this is not guaranteed to run once, and two objects in one
// file would be unparseable for the harness this exists to serve.
//
// v is `any` rather than a concrete usage type because chat imports trace;
// naming chat's type here would be an import cycle.
func WriteUsage(dir string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, usageFileName), append(data, '\n'), 0o644)
}
