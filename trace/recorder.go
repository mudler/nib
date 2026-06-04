package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// fileName is the NDJSON transcript written inside the trace directory.
const fileName = "trace.ndjson"

// Recorder appends LLM call records to <dir>/trace.ndjson. It is safe for
// concurrent use: each Record call is serialized under a mutex so lines never
// interleave. One unbuffered write syscall per record means a process crash
// leaves a valid prefix of complete lines.
type Recorder struct {
	mu sync.Mutex
	f  *os.File
}

// NewRecorder creates dir if needed and opens the transcript for appending.
func NewRecorder(dir string) (*Recorder, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, fileName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Recorder{f: f}, nil
}

// Record marshals rec to a single JSON line and appends it, defaulting the
// timestamp and provider when the caller left them unset.
func (r *Recorder) Record(rec Record) error {
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now()
	}
	if rec.Provider == "" {
		rec.Provider = "openai"
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err = r.f.Write(append(line, '\n'))
	return err
}

// Close closes the underlying file. Safe to call on a nil Recorder (tracing
// disabled).
func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Close()
}
