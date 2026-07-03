// Package catalog discovers installable nib extensions (skills and plugins)
// from agentskills.io-compatible index sources, merges them across sources, and
// installs a chosen entry through the skill/plugin managers. It is GUI-free:
// fetch, parse, crawl, merge, and install only — no terminal or window code.
package catalog

// Kind is the kind of extension an index entry describes.
type Kind string

const (
	// KindSkill is a skill pack (a SKILL.md tree).
	KindSkill Kind = "skill"
	// KindPlugin is a plugin bundle (a manifested contribution set).
	KindPlugin Kind = "plugin"
)

// Meta is the normalized, kind-tagged description of one installable extension,
// whatever its source. Only Kind, Name, and Description are required; the
// install locator is one of GitURL, Repo+Path, or ZipURL. Source and Trust are
// provenance stamped by Merge, never read from an index document.
type Meta struct {
	Kind        Kind     `json:"kind"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category,omitempty"`
	Tags        []string `json:"tags,omitempty"`

	// install locator (one of):
	GitURL string `json:"gitURL,omitempty"` // clone a whole repo…
	Repo   string `json:"repo,omitempty"`   // …or owner/repo + subpath…
	Path   string `json:"path,omitempty"`
	Ref    string `json:"ref,omitempty"`
	ZipURL string `json:"zipURL,omitempty"` // …or download a zip.

	// provenance (filled by Merge, not by the index):
	Source string `json:"source,omitempty"`
	Trust  string `json:"trust,omitempty"`
}
