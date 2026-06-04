package trace

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestRecorderAppendsOneLinePerRecord(t *testing.T) {
	dir := t.TempDir()
	rec, err := NewRecorder(dir)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	if err := rec.Record(Record{Model: "m", Method: "ask", Request: &openai.ChatCompletionRequest{Model: "m"}}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := rec.Record(Record{Model: "m", Method: "chat_completion", Request: &openai.ChatCompletionRequest{Model: "m"}}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.Open(filepath.Join(dir, "trace.ndjson"))
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer f.Close()

	var lines int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var got Record
		if err := json.Unmarshal(sc.Bytes(), &got); err != nil {
			t.Fatalf("line %d not valid JSON: %v", lines, err)
		}
		if got.Provider != "openai" {
			t.Fatalf("line %d: provider = %q, want openai (defaulted)", lines, got.Provider)
		}
		if got.Timestamp.IsZero() {
			t.Fatalf("line %d: timestamp not stamped", lines)
		}
		lines++
	}
	if lines != 2 {
		t.Fatalf("got %d lines, want 2", lines)
	}
}

func TestRecorderConcurrentWritesDoNotInterleave(t *testing.T) {
	dir := t.TempDir()
	rec, err := NewRecorder(dir)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = rec.Record(Record{Model: "m", Method: "ask", Request: &openai.ChatCompletionRequest{Model: "m"}})
		}()
	}
	wg.Wait()
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.Open(filepath.Join(dir, "trace.ndjson"))
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer f.Close()

	var lines int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var got Record
		if err := json.Unmarshal(sc.Bytes(), &got); err != nil {
			t.Fatalf("line %d not valid JSON (interleaved write?): %v", lines, err)
		}
		lines++
	}
	if lines != 50 {
		t.Fatalf("got %d lines, want 50", lines)
	}
}

func TestRecorderCloseNilSafe(t *testing.T) {
	var rec *Recorder
	if err := rec.Close(); err != nil {
		t.Fatalf("nil Close: %v", err)
	}
}
