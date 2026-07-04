package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mudler/nib/catalog"
	"github.com/mudler/nib/plugin"
)

// renderBrowse formats merged metas (already sorted by category then name)
// grouped by category, one block per category separated by a blank line. Each
// entry shows name, description, and a provenance line.
func renderBrowse(metas []catalog.Meta) string {
	if len(metas) == 0 {
		return "No extensions found.\n"
	}
	var b strings.Builder
	lastCat := ""
	first := true
	for _, m := range metas {
		cat := m.Category
		if cat == "" {
			cat = "uncategorized"
		}
		if first || cat != lastCat {
			if !first {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "%s\n", cat)
			lastCat = cat
			first = false
		}
		fmt.Fprintf(&b, "  %-24s %s\n", m.Name, m.Description)
		fmt.Fprintf(&b, "      [%s · %s]\n", m.Source, m.Trust)
	}
	return b.String()
}

// filterMetas returns metas of the given kind, further filtered by a
// case-insensitive substring match on name, description, or any tag when q is
// non-empty.
func filterMetas(metas []catalog.Meta, kind catalog.Kind, q string) []catalog.Meta {
	ql := strings.ToLower(strings.TrimSpace(q))
	var out []catalog.Meta
	for _, m := range metas {
		if m.Kind != kind {
			continue
		}
		if ql == "" ||
			strings.Contains(strings.ToLower(m.Name), ql) ||
			strings.Contains(strings.ToLower(m.Description), ql) ||
			tagMatch(m.Tags, ql) {
			out = append(out, m)
		}
	}
	return out
}

// tagMatch reports whether any tag contains ql (already lowercased).
func tagMatch(tags []string, ql string) bool {
	for _, t := range tags {
		if strings.Contains(strings.ToLower(t), ql) {
			return true
		}
	}
	return false
}

// findCatalogMeta returns the single kind-matching meta named name, erroring on
// no match or on an ambiguous match spanning multiple sources.
func findCatalogMeta(metas []catalog.Meta, kind catalog.Kind, name string) (catalog.Meta, error) {
	var matches []catalog.Meta
	for _, m := range metas {
		if m.Kind == kind && m.Name == name {
			matches = append(matches, m)
		}
	}
	switch len(matches) {
	case 0:
		return catalog.Meta{}, fmt.Errorf("no %s named %q in the catalog", kind, name)
	case 1:
		return matches[0], nil
	default:
		return catalog.Meta{}, fmt.Errorf("%q is ambiguous across sources; disable a source or narrow it", name)
	}
}

// mergeCatalog fetches and merges the configured sources, printing any
// non-fatal per-source errors to stderr, and returns the merged metas.
func mergeCatalog(baseDir string) ([]catalog.Meta, error) {
	sources, err := catalog.LoadSources(baseDir)
	if err != nil {
		return nil, err
	}
	metas, srcErrs := catalog.NewClient().Merge(context.Background(), sources)
	for _, se := range srcErrs {
		fmt.Fprintf(os.Stderr, "warning: %v\n", se)
	}
	return metas, nil
}

// runBrowse prints the merged catalog for a kind, grouped by category.
func runBrowse(baseDir string, kind catalog.Kind) int {
	metas, err := mergeCatalog(baseDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "browse failed: %v\n", err)
		return 1
	}
	fmt.Print(renderBrowse(filterMetas(metas, kind, "")))
	return 0
}

// runSearch prints the merged catalog for a kind, filtered by a query.
func runSearch(baseDir string, kind catalog.Kind, q string) int {
	metas, err := mergeCatalog(baseDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "search failed: %v\n", err)
		return 1
	}
	fmt.Print(renderBrowse(filterMetas(metas, kind, q)))
	return 0
}

// runSource dispatches `<skill|plugin> source <list|add URL|remove LABEL|
// enable LABEL|disable LABEL>`. Sources are kind-agnostic and shared between
// skills and plugins, so this handler is used by both dispatchers.
func runSource(baseDir string, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: nib <skill|plugin> source <list|add URL|remove LABEL|enable LABEL|disable LABEL>")
		return 1
	}
	switch args[0] {
	case "list":
		sources, err := catalog.LoadSources(baseDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "list failed: %v\n", err)
			return 1
		}
		for _, s := range sources {
			state := "disabled"
			if s.Enabled {
				state = "enabled"
			}
			fmt.Printf("%-24s %-9s %-9s %-9s %s\n", s.Label, s.Kind, s.Trust, state, s.URL)
		}
		return 0
	case "add":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: nib <skill|plugin> source add <url>")
			return 1
		}
		s, err := catalog.AddSource(baseDir, args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "add failed: %v\n", err)
			return 1
		}
		fmt.Printf("Added source %q (%s).\n", s.Label, s.Kind)
		return 0
	case "remove", "enable", "disable":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: nib <skill|plugin> source %s <label>\n", args[0])
			return 1
		}
		var err error
		switch args[0] {
		case "remove":
			err = catalog.RemoveSource(baseDir, args[1])
		case "enable":
			err = catalog.SetSourceEnabled(baseDir, args[1], true)
		case "disable":
			err = catalog.SetSourceEnabled(baseDir, args[1], false)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s failed: %v\n", args[0], err)
			return 1
		}
		fmt.Printf("Source %q %sd.\n", args[1], args[0])
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown source command: %s\n", args[0])
		return 1
	}
}

// catalogBaseDir is the nib config base for CLI catalog operations. Wrapped so
// tests can call runSource/runBrowse with an explicit dir.
func catalogBaseDir() string { return plugin.BaseDir() }
