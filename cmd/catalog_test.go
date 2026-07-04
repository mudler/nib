package cmd

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/mudler/nib/catalog"
)

// sortMetasForTest mirrors Merge's ordering (category then name) so the golden
// browse test is stable without a live network fetch.
func sortMetasForTest(m []catalog.Meta) {
	sort.SliceStable(m, func(i, j int) bool {
		if m[i].Category != m[j].Category {
			return m[i].Category < m[j].Category
		}
		return m[i].Name < m[j].Name
	})
}

func TestRenderBrowse_Golden(t *testing.T) {
	// Pre-sorted as Merge returns them (category then name).
	metas := []catalog.Meta{
		{Kind: catalog.KindSkill, Name: "brainstorming", Description: "plan first", Category: "workflow", Source: "bundled", Trust: "official"},
		{Kind: catalog.KindSkill, Name: "zenmode", Description: "focus", Category: "workflow", Source: "acme.dev", Trust: "community"},
		{Kind: catalog.KindSkill, Name: "greeter", Description: "says hi", Category: "chat", Source: "bundled", Trust: "official"},
	}
	// Sort exactly as the pipeline does before rendering.
	sortMetasForTest(metas)
	got := renderBrowse(metas)
	want := "" +
		"chat\n" +
		"  greeter                  says hi\n" +
		"      [bundled · official]\n" +
		"\n" +
		"workflow\n" +
		"  brainstorming            plan first\n" +
		"      [bundled · official]\n" +
		"  zenmode                  focus\n" +
		"      [acme.dev · community]\n"
	if got != want {
		t.Fatalf("renderBrowse mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderBrowse_Empty(t *testing.T) {
	if got := renderBrowse(nil); got != "No extensions found.\n" {
		t.Fatalf("empty render = %q", got)
	}
}

func TestFilterMetas(t *testing.T) {
	metas := []catalog.Meta{
		{Kind: catalog.KindSkill, Name: "greeter", Description: "says hi", Tags: []string{"chat"}},
		{Kind: catalog.KindPlugin, Name: "gitplug", Description: "git tools"},
		{Kind: catalog.KindSkill, Name: "farewell", Description: "says bye"},
	}
	if got := filterMetas(metas, catalog.KindSkill, ""); len(got) != 2 {
		t.Fatalf("kind filter: want 2 skills, got %d", len(got))
	}
	if got := filterMetas(metas, catalog.KindSkill, "hi"); len(got) != 1 || got[0].Name != "greeter" {
		t.Fatalf("query on description failed: %+v", got)
	}
	if got := filterMetas(metas, catalog.KindSkill, "chat"); len(got) != 1 || got[0].Name != "greeter" {
		t.Fatalf("query on tag failed: %+v", got)
	}
}

func TestFindCatalogMeta(t *testing.T) {
	metas := []catalog.Meta{
		{Kind: catalog.KindSkill, Name: "greeter", Source: "A"},
		{Kind: catalog.KindSkill, Name: "greeter", Source: "B"}, // ambiguous
		{Kind: catalog.KindPlugin, Name: "gitplug", Source: "A"},
	}
	if _, err := findCatalogMeta(metas, catalog.KindSkill, "greeter"); err == nil {
		t.Fatal("expected ambiguity error")
	}
	if _, err := findCatalogMeta(metas, catalog.KindSkill, "missing"); err == nil {
		t.Fatal("expected not-found error")
	}
	m, err := findCatalogMeta(metas, catalog.KindPlugin, "gitplug")
	if err != nil || m.Source != "A" {
		t.Fatalf("unique lookup failed: %+v err=%v", m, err)
	}
}

func TestRunSource_AddListRemove(t *testing.T) {
	base := t.TempDir()
	if code := runSource(base, []string{"add", "https://acme.dev/index.json"}); code != 0 {
		t.Fatalf("source add exit=%d", code)
	}
	if _, err := os.Stat(filepath.Join(base, "sources.yaml")); err != nil {
		t.Fatalf("add did not persist: %v", err)
	}
	if code := runSource(base, []string{"disable", "acme.dev"}); code != 0 {
		t.Fatalf("source disable exit=%d", code)
	}
	if code := runSource(base, []string{"remove", "acme.dev"}); code != 0 {
		t.Fatalf("source remove exit=%d", code)
	}
	if code := runSource(base, []string{"remove", "bundled"}); code == 0 {
		t.Fatal("removing bundled must fail with a nonzero exit")
	}
	if code := runSource(base, []string{"bogus"}); code == 0 {
		t.Fatal("unknown source subcommand must fail")
	}
}
