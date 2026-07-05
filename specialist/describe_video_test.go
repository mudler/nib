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

func TestDescribeVideo(t *testing.T) {
	var sawVideoPart bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content []struct {
					Type     string `json:"type"`
					VideoURL *struct {
						URL string `json:"url"`
					} `json:"video_url"`
				} `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		for _, m := range req.Messages {
			for _, part := range m.Content {
				if part.Type == "video_url" && part.VideoURL != nil && strings.HasPrefix(part.VideoURL.URL, "data:video/") {
					sawVideoPart = true
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"a person waving"}}]}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	vid := filepath.Join(dir, "clip.mp4")
	_ = os.WriteFile(vid, []byte("\x00\x00\x00\x18ftypmp42fakevideo"), 0o644)

	got, err := New(srv.URL, "").DescribeVideo(context.Background(), vid, "", "")
	if err != nil {
		t.Fatalf("DescribeVideo: %v", err)
	}
	if !strings.Contains(got, "waving") {
		t.Fatalf("unexpected description %q", got)
	}
	if !sawVideoPart {
		t.Fatal("expected a data:video/ video_url part")
	}
}
