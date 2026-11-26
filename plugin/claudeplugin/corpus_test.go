package claudeplugin

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRealCorpus parses every plugin found under the directories listed in
// CLAUDEPLUGIN_CORPUS (separated by the OS list separator). Each directory is
// a clone of a real plugin repository or marketplace. The test skips when
// the variable is unset. It fails on a load error and logs the counts.
//
//	CLAUDEPLUGIN_CORPUS=/path/to/clones/a:/path/to/clones/b go test -run RealCorpus -v ./plugin/claudeplugin
func TestRealCorpus(t *testing.T) {
	env := os.Getenv("CLAUDEPLUGIN_CORPUS")
	if env == "" {
		t.Skip("CLAUDEPLUGIN_CORPUS not set")
	}
	var total struct {
		marketplaces, entries, remote, unsupported, plugins, errors int
		fetched, fetchErrors                                        int
		skills, commands, agents, hooks, mcp, lsp, bin, runsCode    int
		skipped                                                     map[string]int
	}
	total.skipped = map[string]int{}
	// CLAUDEPLUGIN_CORPUS_FETCH=N also fetches the first N remote entries.
	fetchLeft, _ := strconv.Atoi(os.Getenv("CLAUDEPLUGIN_CORPUS_FETCH"))
	for _, repo := range filepath.SplitList(env) {
		if !isDir(repo) {
			t.Logf("%s: missing, skipped", repo)
			continue
		}
		loaded := map[string]bool{}
		load := func(dir string, opts LoadOptions) {
			p, err := Load(dir, opts)
			if err != nil {
				total.errors++
				t.Errorf("%s: %v", dir, err)
				return
			}
			loaded[p.Root] = true
			total.plugins++
			total.skills += len(p.Skills)
			total.commands += len(p.Commands)
			total.agents += len(p.Agents)
			total.hooks += len(p.Hooks)
			total.mcp += len(p.MCPServers)
			total.lsp += len(p.LSPServers)
			total.bin += len(p.Bin)
			if p.RunsCode() {
				total.runsCode++
			}
			for _, s := range p.Skipped {
				total.skipped[string(s.Code)+" "+s.Component+": "+reasonKey(s.Reason)]++
			}
		}
		// Every marketplace under the clone, with its entries.
		_ = filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() || d.Name() == ".git" || d.Name() == "node_modules" {
				if d != nil && d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
					return filepath.SkipDir
				}
				return nil
			}
			if !IsMarketplace(p) {
				return nil
			}
			m, err := LoadMarketplace(p)
			if err != nil {
				total.errors++
				t.Errorf("%s: %v", p, err)
				return nil
			}
			total.marketplaces++
			for _, s := range m.Skipped {
				total.skipped["entry: "+reasonKey(s.Reason)]++
			}
			for i := range m.Plugins {
				e := &m.Plugins[i]
				total.entries++
				if err := e.Source.Supported(); err != nil {
					total.unsupported++
					continue
				}
				if e.Source.Kind != SourceRelative {
					total.remote++
					if fetchLeft > 0 {
						fetchLeft--
						dest := filepath.Join(t.TempDir(), "p")
						ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
						res, err := Fetch(ctx, m, e, dest, FetchOptions{MaxBytes: 200 << 20})
						cancel()
						if err != nil {
							total.fetchErrors++
							t.Logf("fetch %s@%s (%s): %v", e.Name, m.Name, e.Source, err)
							continue
						}
						total.fetched++
						if len(res.SHA) != 40 {
							t.Errorf("fetch %s: no SHA", e.Name)
						}
						load(dest, LoadOptions{Entry: e, Marketplace: m.Name})
					}
					continue
				}
				dir, err := m.RelativeDir(e.Source)
				if err != nil || !isDir(dir) {
					total.errors++
					t.Errorf("%s: entry %s: %v", p, e.Name, err)
					continue
				}
				load(dir, LoadOptions{Entry: e, Marketplace: m.Name})
			}
			return nil
		})
		// Plugins with a manifest that no marketplace lists.
		_ = filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			if isFile(filepath.Join(p, ManifestPath)) {
				if abs, _ := filepath.Abs(p); !loaded[abs] {
					load(p, LoadOptions{})
				}
			}
			return nil
		})
	}
	t.Logf("marketplaces=%d entries=%d (remote=%d, unsupported source=%d)",
		total.marketplaces, total.entries, total.remote, total.unsupported)
	t.Logf("remote entries fetched=%d fetch errors=%d", total.fetched, total.fetchErrors)
	t.Logf("plugins parsed=%d errors=%d runs-code=%d", total.plugins, total.errors, total.runsCode)
	t.Logf("skills=%d commands=%d agents=%d hook-groups=%d mcp=%d lsp=%d bin=%d",
		total.skills, total.commands, total.agents, total.hooks, total.mcp, total.lsp, total.bin)
	keys := make([]string, 0, len(total.skipped))
	for k := range total.skipped {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("skipped %4d  %s", total.skipped[k], k)
	}
}

// reasonKey shortens a reason to its stable part for grouping.
func reasonKey(r string) string {
	if i := strings.Index(r, ": "); i > 0 && strings.Contains(r[:i], "/") {
		r = r[i+2:]
	}
	if len(r) > 70 {
		r = r[:70]
	}
	return r
}
