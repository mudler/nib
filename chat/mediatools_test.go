package chat

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveWorkspacePath(t *testing.T) {
	if got := resolveWorkspacePath("/work", "a.png"); got != filepath.Join("/work", "a.png") {
		t.Fatalf("relative should join workingDir, got %q", got)
	}
	if got := resolveWorkspacePath("/work", "/abs/a.png"); got != "/abs/a.png" {
		t.Fatalf("absolute should be unchanged, got %q", got)
	}
	if got := resolveWorkspacePath("", "a.png"); got != "a.png" {
		t.Fatalf("empty workingDir should leave path, got %q", got)
	}
}

func TestReadImageToolRun(t *testing.T) {
	var gotPath, gotQ string
	tool := &readImageTool{describe: func(path, question string) (string, error) {
		gotPath, gotQ = path, question
		return "a red square", nil
	}}
	res, _, err := tool.Run(map[string]any{"path": "x.png", "question": "what color?"})
	if err != nil || res != "a red square" {
		t.Fatalf("run: res=%q err=%v", res, err)
	}
	if gotPath != "x.png" || gotQ != "what color?" {
		t.Fatalf("args not threaded: %q %q", gotPath, gotQ)
	}
	// empty path → error result, delegate not called
	res, _, _ = tool.Run(map[string]any{})
	if res == "a red square" || res == "" {
		t.Fatalf("empty path should return an error result, got %q", res)
	}
	// gotPath must still hold the prior value: the empty-path guard skips the
	// delegate rather than invoking it with an empty path.
	if gotPath != "x.png" {
		t.Fatalf("empty-path guard should skip the delegate, but gotPath=%q", gotPath)
	}
}

func TestTranscribeAudioToolRun(t *testing.T) {
	var gotPath string
	tool := &transcribeAudioTool{transcribe: func(path string) (string, error) {
		gotPath = path
		return "hello world", nil
	}}
	res, _, err := tool.Run(map[string]any{"path": "a.wav"})
	if err != nil || res != "hello world" {
		t.Fatalf("run: res=%q err=%v", res, err)
	}
	if gotPath != "a.wav" {
		t.Fatalf("path not threaded: %q", gotPath)
	}
	// empty path → error result, delegate not called
	res, _, _ = tool.Run(map[string]any{})
	if !strings.Contains(res, "required") {
		t.Fatalf("empty path should return a 'required' error result, got %q", res)
	}
	if gotPath != "a.wav" {
		t.Fatalf("empty-path guard should skip the delegate, but gotPath=%q", gotPath)
	}
	// delegate error → failed result
	errTool := &transcribeAudioTool{transcribe: func(path string) (string, error) {
		return "", errors.New("boom")
	}}
	res, _, _ = errTool.Run(map[string]any{"path": "a.wav"})
	if !strings.Contains(res, "failed") {
		t.Fatalf("delegate error should return a 'failed' result, got %q", res)
	}
}

func TestReadVideoToolRun(t *testing.T) {
	var gotPath, gotQ string
	tool := &readVideoTool{describe: func(path, question string) (string, error) {
		gotPath, gotQ = path, question
		return "a person waving", nil
	}}
	res, _, _ := tool.Run(map[string]any{"path": "c.mp4", "question": "what happens?"})
	if res != "a person waving" {
		t.Fatalf("run: %q", res)
	}
	if gotPath != "c.mp4" || gotQ != "what happens?" {
		t.Fatalf("args not threaded: %q %q", gotPath, gotQ)
	}
	// empty path → error result, delegate not called
	res, _, _ = tool.Run(map[string]any{})
	if !strings.Contains(res, "required") {
		t.Fatalf("empty path should return a 'required' error result, got %q", res)
	}
	if gotPath != "c.mp4" {
		t.Fatalf("empty-path guard should skip the delegate, but gotPath=%q", gotPath)
	}
	// delegate error → failed result
	errTool := &readVideoTool{describe: func(path, question string) (string, error) {
		return "", errors.New("boom")
	}}
	res, _, _ = errTool.Run(map[string]any{"path": "c.mp4"})
	if !strings.Contains(res, "failed") {
		t.Fatalf("delegate error should return a 'failed' result, got %q", res)
	}
}

func TestMediaToolsAreReadOnly(t *testing.T) {
	var noCmds readOnlyCommands
	for _, n := range []string{"read_image", "transcribe_audio", "read_video"} {
		if !IsReadOnly(n, "{}", noCmds) {
			t.Fatalf("%s should be read-only (auto-approve)", n)
		}
	}
}
