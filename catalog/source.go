package catalog

import "strings"

// Source kind constants: how a source's index is obtained.
const (
	// SourceIndex is a URL ending in .json, fetched verbatim.
	SourceIndex = "index"
	// SourceWellKnown is a bare host exposing /.well-known/skills/index.json.
	SourceWellKnown = "wellknown"
	// SourceGitHub is a github.com repo, resolved index-first with a crawl fallback.
	SourceGitHub = "github"
	// SourceBundled is the index compiled into the binary (not user-addable).
	SourceBundled = "bundled"
)

// Source is a place Metas come from. Kind (auto-detected from URL for user-added
// sources) drives how the index is obtained. Label is the display name and the
// dedup/removal key.
type Source struct {
	Label   string `yaml:"label" json:"label"`
	URL     string `yaml:"url" json:"url"`
	Kind    string `yaml:"kind" json:"kind"`   // index|wellknown|github|bundled
	Trust   string `yaml:"trust" json:"trust"` // official|community
	Enabled bool   `yaml:"enabled" json:"enabled"`
}

// DetectSourceKind classifies a user-pasted source URL and derives a display
// label. A URL ending in .json is a verbatim index; a github.com/… or git@…
// URL is a GitHub source; anything else is treated as a bare host exposing a
// /.well-known/skills/index.json.
func DetectSourceKind(url string) (kind, label string) {
	u := strings.TrimSpace(url)
	switch {
	case strings.HasSuffix(strings.ToLower(u), ".json"):
		return SourceIndex, hostLabel(u)
	case strings.Contains(u, "github.com/") || strings.HasPrefix(u, "git@"):
		return SourceGitHub, repoLabel(u)
	default:
		return SourceWellKnown, hostLabel(u)
	}
}

// parseGitHubRepo extracts owner and repo from a github.com URL or an scp-style
// git@github.com:owner/repo(.git) URL. ok is false if it is not recognizable.
func parseGitHubRepo(url string) (owner, repo string, ok bool) {
	u := strings.TrimSpace(url)
	u = strings.TrimSuffix(u, "/")
	u = strings.TrimSuffix(u, ".git")
	switch {
	case strings.HasPrefix(u, "git@github.com:"):
		u = strings.TrimPrefix(u, "git@github.com:")
	case strings.Contains(u, "github.com/"):
		_, u, _ = strings.Cut(u, "github.com/")
	default:
		return "", "", false
	}
	parts := strings.Split(u, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// repoLabel returns "owner/repo" for a github URL, or the raw URL if it does not
// parse as one.
func repoLabel(url string) string {
	if owner, repo, ok := parseGitHubRepo(url); ok {
		return owner + "/" + repo
	}
	return url
}

// hostLabel returns the host portion of a URL (scheme and path stripped) for use
// as a display label.
func hostLabel(raw string) string {
	r := strings.TrimSpace(raw)
	if i := strings.Index(r, "://"); i >= 0 {
		r = r[i+3:]
	}
	if i := strings.IndexByte(r, '/'); i >= 0 {
		r = r[:i]
	}
	return r
}
