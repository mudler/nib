package specialist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDescribe(t *testing.T) {
	var sawImagePart bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content []struct {
					Type     string `json:"type"`
					ImageURL *struct {
						URL string `json:"url"`
					} `json:"image_url"`
				} `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		for _, m := range req.Messages {
			for _, part := range m.Content {
				if part.Type == "image_url" && part.ImageURL != nil && strings.HasPrefix(part.ImageURL.URL, "data:image/") {
					sawImagePart = true
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"a red square"}}]}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	img := filepath.Join(dir, "x.png")
	_ = os.WriteFile(img, []byte("\x89PNG\r\n\x1a\nfakepng"), 0o644)

	got, err := New(srv.URL, "").Describe(context.Background(), img, "", "")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if !strings.Contains(got, "red square") {
		t.Fatalf("unexpected description %q", got)
	}
	if !sawImagePart {
		t.Fatal("expected a data:image/ image_url part")
	}
}
