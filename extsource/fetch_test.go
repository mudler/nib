package extsource

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFetchSKILLURL_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("---\nname: n\ndescription: d\n---\nbody"))
	}))
	defer srv.Close()

	dest := t.TempDir()
	if err := FetchSKILLURL(srv.URL+"/SKILL.md", dest); err != nil {
		t.Fatalf("FetchSKILLURL: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dest, "SKILL.md"))
	if err != nil || string(b) == "" {
		t.Fatalf("SKILL.md not written: %v", err)
	}
}

func TestFetchSKILLURL_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	if err := FetchSKILLURL(srv.URL+"/SKILL.md", t.TempDir()); err == nil {
		t.Fatal("expected error on 404")
	}
}
