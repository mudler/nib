package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitHubCrawl_Fallback(t *testing.T) {
	mux := http.NewServeMux()
	// No index files exist → both raw index probes 404, forcing a crawl.
	mux.HandleFunc("/raw/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/raw/o/r/main/skills/greeter/SKILL.md":
			w.Write([]byte("---\nname: greeter\ndescription: says hi\n---\nbody"))
		case "/raw/o/r/main/plugins/gitplug/nib-plugin.yaml":
			w.Write([]byte("name: gitplug\nversion: 1.0.0\ndescription: git tools\n"))
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	})
	mux.HandleFunc("/api/repos/o/r/git/trees/main", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tree":[
			{"path":"skills/greeter","type":"tree"},
			{"path":"skills/greeter/SKILL.md","type":"blob"},
			{"path":"plugins/gitplug","type":"tree"},
			{"path":"plugins/gitplug/nib-plugin.yaml","type":"blob"},
			{"path":"README.md","type":"blob"}
		],"truncated":false}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(srv)

	got, err := c.Fetch(context.Background(), Source{Kind: SourceGitHub, URL: "https://github.com/o/r"})
	if err != nil {
		t.Fatalf("crawl fetch: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 synthesized metas, got %d: %+v", len(got), got)
	}
	byName := map[string]Meta{}
	for _, m := range got {
		byName[m.Name] = m
	}
	skill, ok := byName["greeter"]
	if !ok || skill.Kind != KindSkill || skill.Repo != "o/r" || skill.Path != "skills/greeter" ||
		skill.Ref != "main" || skill.Description != "says hi" {
		t.Fatalf("skill meta wrong: %+v", skill)
	}
	plug, ok := byName["gitplug"]
	if !ok || plug.Kind != KindPlugin || plug.Path != "plugins/gitplug" || plug.Description != "git tools" {
		t.Fatalf("plugin meta wrong: %+v", plug)
	}
}

// TestGitHubCrawl_BranchProbe verifies the crawl falls through from a 404 on the
// main branch tree to master, and that a directory whose SKILL.md cannot be
// fetched still yields a Meta named from the directory basename.
func TestGitHubCrawl_BranchProbe(t *testing.T) {
	mux := http.NewServeMux()
	// No raw files resolve (all index probes and blob fetches 404).
	mux.HandleFunc("/raw/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusNotFound)
	})
	// main tree 404s → probe master next.
	mux.HandleFunc("/api/repos/o/r/git/trees/main", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusNotFound)
	})
	mux.HandleFunc("/api/repos/o/r/git/trees/master", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tree":[
			{"path":"skills/solo/SKILL.md","type":"blob"}
		],"truncated":false}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(srv)

	got, err := c.Fetch(context.Background(), Source{Kind: SourceGitHub, URL: "https://github.com/o/r"})
	if err != nil {
		t.Fatalf("crawl fetch: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 meta from master branch, got %d: %+v", len(got), got)
	}
	m := got[0]
	// The SKILL.md blob 404s, so name falls back to the directory basename.
	if m.Kind != KindSkill || m.Name != "solo" || m.Ref != "master" || m.Path != "skills/solo" {
		t.Fatalf("branch-probe meta wrong: %+v", m)
	}
}

// TestGitHubCrawl_RateLimited verifies a non-200 on every branch tree (e.g. a
// rate-limit response) is surfaced as an error rather than an empty result.
func TestGitHubCrawl_RateLimited(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/raw/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusNotFound)
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(srv)

	if _, err := c.Fetch(context.Background(), Source{Kind: SourceGitHub, URL: "https://github.com/o/r"}); err == nil {
		t.Fatal("expected error when every branch tree is non-200")
	}
}
