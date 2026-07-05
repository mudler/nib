package slash

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAtPaths(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "report.pdf")
	_ = os.WriteFile(real, []byte("x"), 0o644)

	text, files := parseAtPaths("summarize @" + real + " for @someone please")
	if len(files) != 1 || files[0] != real {
		t.Fatalf("expected the real file attached, got %v", files)
	}
	if text != "summarize  for @someone please" && text != "summarize for @someone please" {
		t.Fatalf("real @path stripped, literal @someone kept; got %q", text)
	}
}

func TestResolveAttach(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.wav")
	_ = os.WriteFile(f, []byte("x"), 0o644)

	if a := Resolve("/attach "+f, nil, nil, nil); a.Kind != KindAttach || a.AttachOp != AttachStage || a.AttachPath != f || a.Transcribe {
		t.Fatalf("plain stage: %+v", a)
	}
	if a := Resolve("/attach -t "+f, nil, nil, nil); a.AttachOp != AttachStage || !a.Transcribe {
		t.Fatalf("-t stage: %+v", a)
	}
	if a := Resolve("/attach", nil, nil, nil); a.AttachOp != AttachList {
		t.Fatalf("list: %+v", a)
	}
	if a := Resolve("/attach clear", nil, nil, nil); a.AttachOp != AttachClear {
		t.Fatalf("clear: %+v", a)
	}
	if a := Resolve("/attach /no/such/file", nil, nil, nil); a.Kind != KindError {
		t.Fatalf("missing file must be KindError: %+v", a)
	}
}
