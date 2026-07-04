package cmd

import (
	"testing"

	"github.com/mudler/nib/catalog"
)

func TestPluginBrowseFilter(t *testing.T) {
	// filterMetas with KindPlugin must exclude skills — the plugin browse view.
	metas := []catalog.Meta{
		{Kind: catalog.KindSkill, Name: "greeter"},
		{Kind: catalog.KindPlugin, Name: "gitplug"},
	}
	got := filterMetas(metas, catalog.KindPlugin, "")
	if len(got) != 1 || got[0].Name != "gitplug" {
		t.Fatalf("plugin filter wrong: %+v", got)
	}
}

func TestPluginSourceShared(t *testing.T) {
	// `plugin source` shares sources.yaml with `skill source`.
	base := t.TempDir()
	if code := runSource(base, []string{"add", "https://acme.dev/index.json"}); code != 0 {
		t.Fatalf("plugin source add exit=%d", code)
	}
	sources, _ := catalog.LoadSources(base)
	if findSourceCmd(sources, "acme.dev") == nil {
		t.Fatal("source added via plugin path not visible")
	}
}

// findSourceCmd is a local helper (avoid clashing with catalog_test.go's findSource).
func findSourceCmd(sources []catalog.Source, label string) *catalog.Source {
	for i := range sources {
		if sources[i].Label == label {
			return &sources[i]
		}
	}
	return nil
}
