package specialist

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTranscribe(t *testing.T) {
	var gotModel, gotFile string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		gotModel = r.FormValue("model")
		if f, _, err := r.FormFile("file"); err == nil {
			gotFile = "present"
			_ = f
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hello from parakeet"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	audio := filepath.Join(dir, "clip.wav")
	_ = os.WriteFile(audio, []byte("RIFFxxxxWAVE"), 0o644)

	got, err := New(srv.URL, "").Transcribe(context.Background(), audio, "")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if !strings.Contains(got, "hello from parakeet") {
		t.Fatalf("unexpected transcript %q", got)
	}
	if gotFile != "present" {
		t.Fatal("expected multipart file field")
	}
	if gotModel != "" {
		t.Fatalf("expected empty model (auto-select), got %q", gotModel)
	}
}
