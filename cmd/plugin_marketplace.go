package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mudler/nib/catalog"
	"github.com/mudler/nib/plugin"
)

// pluginMarketplace dispatches `plugin marketplace <add|list|update|remove>`.
func pluginMarketplace(prog string, mgr *plugin.Manager, args []string) int {
	usage := func() int {
		fmt.Fprintf(os.Stderr, "usage: %s plugin marketplace <add SOURCE|list|update [NAME]|remove NAME>\n", prog)
		return 1
	}
	if len(args) == 0 {
		return usage()
	}
	ctx := context.Background()
	switch args[0] {
	case "add":
		if len(args) != 2 {
			return usage()
		}
		return addMarketplaceAndList(prog, mgr, args[1])
	case "list":
		recs, err := mgr.Marketplaces()
		if err != nil {
			fmt.Fprintf(os.Stderr, "list failed: %v\n", err)
			return 1
		}
		if len(recs) == 0 {
			fmt.Println("No marketplaces registered.")
			return 0
		}
		for _, r := range recs {
			sha := r.SHA
			if len(sha) > 12 {
				sha = sha[:12]
			}
			fmt.Printf("%-24s %-12s %s\n", r.Name, sha, r.Source)
		}
		return 0
	case "update":
		names := args[1:]
		if len(names) == 0 {
			recs, err := mgr.Marketplaces()
			if err != nil {
				fmt.Fprintf(os.Stderr, "update failed: %v\n", err)
				return 1
			}
			for _, r := range recs {
				names = append(names, r.Name)
			}
		}
		code := 0
		for _, n := range names {
			m, err := mgr.UpdateMarketplace(ctx, n)
			if err != nil {
				fmt.Fprintf(os.Stderr, "update %s failed: %v\n", n, err)
				code = 1
				continue
			}
			fmt.Printf("Marketplace %q updated (%d plugins).\n", m.Name, len(m.Plugins))
		}
		return code
	case "remove":
		if len(args) != 2 {
			return usage()
		}
		if err := mgr.RemoveMarketplace(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "remove failed: %v\n", err)
			return 1
		}
		fmt.Printf("Marketplace %q removed. Its installed plugins stay installed.\n", args[1])
		return 0
	}
	return usage()
}

// addMarketplaceAndList registers a marketplace and prints its plugins with
// the command that installs each.
func addMarketplaceAndList(prog string, mgr *plugin.Manager, src string) int {
	m, err := mgr.AddMarketplace(context.Background(), src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marketplace add failed: %v\n", err)
		return 1
	}
	fmt.Printf("Added marketplace %q with %d plugin(s). Install one with: %s plugin install <name>@%s\n", m.Name, len(m.Plugins), prog, m.Name)
	for _, e := range m.Plugins {
		note := ""
		if err := e.Source.Supported(); err != nil {
			note = " (" + string(e.Source.Kind) + " source: not supported)"
		}
		fmt.Printf("  %-28s %s%s\n", e.Name+"@"+m.Name, e.Description, note)
	}
	for _, s := range m.Skipped {
		fmt.Fprintf(os.Stderr, "  skipped %s: %s\n", s.Name, s.Reason)
	}
	return 0
}

func marketplaceRegistered(mgr *plugin.Manager, name string) bool {
	recs, err := mgr.Marketplaces()
	if err != nil {
		return false
	}
	for _, r := range recs {
		if r.Name == name {
			return true
		}
	}
	return false
}

// marketplaceMetas lists the plugins of the registered marketplaces as
// catalog entries named name@marketplace.
func marketplaceMetas(mgr *plugin.Manager) []catalog.Meta {
	recs, err := mgr.Marketplaces()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: marketplaces: %v\n", err)
		return nil
	}
	var out []catalog.Meta
	for _, r := range recs {
		m, err := mgr.Marketplace(r.Name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: marketplace %s: %v\n", r.Name, err)
			continue
		}
		for _, e := range m.Plugins {
			if e.Source.Supported() != nil {
				continue
			}
			out = append(out, catalog.Meta{
				Kind:        catalog.KindPlugin,
				Name:        e.Name + "@" + m.Name,
				Description: e.Description,
				Category:    e.Category,
				Tags:        append(append([]string{}, e.Tags...), e.Keywords...),
				Source:      "marketplace " + m.Name,
				Trust:       "community",
			})
		}
	}
	return out
}

// runPluginBrowse prints catalog plugins and the plugins of registered
// marketplaces, filtered by q when it is not empty.
func runPluginBrowse(mgr *plugin.Manager, root, q string) int {
	metas, err := mergeCatalog(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "browse failed: %v\n", err)
		return 1
	}
	metas = append(metas, marketplaceMetas(mgr)...)
	fmt.Print(renderBrowse(filterMetas(metas, catalog.KindPlugin, strings.TrimSpace(q))))
	return 0
}
