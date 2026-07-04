package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/mudler/nib/plugin"
)

// treeResponse is the subset of the GitHub git-trees API we read.
type treeResponse struct {
	Tree      []treeEntry `json:"tree"`
	Truncated bool        `json:"truncated"`
}

// treeEntry is one node in a recursive git-tree listing.
type treeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"` // "blob" | "tree"
}

// githubCrawl lists a repo's tree via the GitHub API and synthesizes a Meta for
// every directory containing a SKILL.md (skill) or a native plugin manifest
// (plugin), reading each for its name/description. It is best-effort and
// unauthenticated: any error (including rate limiting) is returned so Merge can
// surface it as a non-fatal per-source error.
func (c *Client) githubCrawl(ctx context.Context, owner, repo string) ([]Meta, error) {
	var lastErr error
	for _, br := range c.Branches {
		api := fmt.Sprintf("%s/repos/%s/%s/git/trees/%s?recursive=1", c.APIBase, owner, repo, br)
		data, err := c.getBytes(ctx, api, 20<<20)
		if err != nil {
			lastErr = err
			continue
		}
		var tr treeResponse
		if err := json.Unmarshal(data, &tr); err != nil {
			lastErr = err
			continue
		}
		return c.metasFromTree(ctx, owner, repo, br, tr.Tree), nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no reachable branch for %s/%s", owner, repo)
	}
	return nil, lastErr
}

// marker is a recognized extension marker file found in the tree, before subtree
// pruning: its containing directory, the blob's full path, and its Kind.
type marker struct {
	dir      string
	blobPath string
	kind     Kind
}

// metasFromTree turns a flat tree listing into one Meta per extension directory.
// A SKILL.md marks a skill; a native plugin manifest marks a plugin. Name and
// description come from the marker file (best-effort — a fetch failure leaves
// the name as the directory's basename).
//
// Nested markers are pruned: once a directory is recognized as an extension, a
// marker file in any strictly-nested subdirectory is skipped — mirroring
// skill.HarvestPack, where a skill's own references/examples cannot define
// further skills (e.g. skills/foo/references/SKILL.md must not yield a bogus
// "references" extension alongside the real skills/foo). The prune is
// order-independent: a directory survives only if no OTHER recognized marker
// directory is a strict ancestor of it.
func (c *Client) metasFromTree(ctx context.Context, owner, repo, branch string, tree []treeEntry) []Meta {
	var markers []marker
	dirs := map[string]struct{}{}
	for _, e := range tree {
		if e.Type != "blob" {
			continue
		}
		dir, file := path.Split(e.Path)
		dir = strings.TrimSuffix(dir, "/")
		switch file {
		case "SKILL.md":
			markers = append(markers, marker{dir: dir, blobPath: e.Path, kind: KindSkill})
			dirs[dir] = struct{}{}
		case plugin.NativeManifestFile:
			markers = append(markers, marker{dir: dir, blobPath: e.Path, kind: KindPlugin})
			dirs[dir] = struct{}{}
		}
	}

	var out []Meta
	for _, mk := range markers {
		if nestedUnder(mk.dir, dirs) {
			continue // pruned: an ancestor directory is itself a recognized extension
		}
		m := Meta{Kind: mk.kind, Repo: owner + "/" + repo, Path: mk.dir, Ref: branch}
		switch mk.kind {
		case KindSkill:
			c.fillFromSkillMd(ctx, owner, repo, branch, mk.blobPath, &m)
		case KindPlugin:
			c.fillFromPluginManifest(ctx, owner, repo, branch, mk.blobPath, &m)
		}
		out = append(out, defaultName(m, mk.dir))
	}
	return out
}

// nestedUnder reports whether some OTHER recognized directory in dirs is a
// strict ancestor of dir (i.e. dir lives inside another extension's subtree).
func nestedUnder(dir string, dirs map[string]struct{}) bool {
	for r := range dirs {
		if r != dir && strings.HasPrefix(dir, r+"/") {
			return true
		}
	}
	return false
}

// defaultName sets a Meta's Name to the directory basename when the marker file
// did not supply one.
func defaultName(m Meta, dir string) Meta {
	if m.Name == "" {
		m.Name = path.Base(dir)
	}
	return m
}

// fillFromSkillMd fetches a SKILL.md blob and fills the Meta's name/description
// from its front-matter, ignoring fetch/parse errors (best-effort).
func (c *Client) fillFromSkillMd(ctx context.Context, owner, repo, branch, blobPath string, m *Meta) {
	raw := fmt.Sprintf("%s/%s/%s/%s/%s", c.RawBase, owner, repo, branch, blobPath)
	data, err := c.getBytes(ctx, raw, 1<<20)
	if err != nil {
		return
	}
	name, desc, _, _ := plugin.ParseSkillMarkdown(data)
	if name != "" {
		m.Name = name
	}
	m.Description = desc
}

// fillFromPluginManifest fetches a native plugin manifest blob and fills the
// Meta's name/description, ignoring fetch/parse errors (best-effort).
func (c *Client) fillFromPluginManifest(ctx context.Context, owner, repo, branch, blobPath string, m *Meta) {
	raw := fmt.Sprintf("%s/%s/%s/%s/%s", c.RawBase, owner, repo, branch, blobPath)
	data, err := c.getBytes(ctx, raw, 1<<20)
	if err != nil {
		return
	}
	mani, err := plugin.ParseManifest(data)
	if err != nil {
		return
	}
	if mani.Name != "" {
		m.Name = mani.Name
	}
	m.Description = mani.Description
}
