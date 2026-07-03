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

// metasFromTree turns a flat tree listing into one Meta per extension directory.
// A SKILL.md marks a skill; a native plugin manifest marks a plugin. Name and
// description come from the marker file (best-effort — a fetch failure leaves
// the name as the directory's basename).
func (c *Client) metasFromTree(ctx context.Context, owner, repo, branch string, tree []treeEntry) []Meta {
	var out []Meta
	for _, e := range tree {
		if e.Type != "blob" {
			continue
		}
		dir, file := path.Split(e.Path)
		dir = strings.TrimSuffix(dir, "/")
		switch file {
		case "SKILL.md":
			m := Meta{Kind: KindSkill, Repo: owner + "/" + repo, Path: dir, Ref: branch}
			c.fillFromSkillMd(ctx, owner, repo, branch, e.Path, &m)
			out = append(out, defaultName(m, dir))
		case plugin.NativeManifestFile:
			m := Meta{Kind: KindPlugin, Repo: owner + "/" + repo, Path: dir, Ref: branch}
			c.fillFromPluginManifest(ctx, owner, repo, branch, e.Path, &m)
			out = append(out, defaultName(m, dir))
		}
	}
	return out
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
