package claudeplugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// MarketplacePath is the marketplace file location inside a marketplace
// directory (the marketplace root).
const MarketplacePath = ".claude-plugin/marketplace.json"

// SourceKind is the type of a plugin source in a marketplace entry.
type SourceKind string

// Plugin source kinds. Relative, GitHub, URL and GitSubdir are supported by
// [Fetch]; the others are parsed and reported as unsupported.
const (
	SourceRelative  SourceKind = "relative"
	SourceGitHub    SourceKind = "github"
	SourceURL       SourceKind = "url"
	SourceGitSubdir SourceKind = "git-subdir"
	SourceNPM       SourceKind = "npm"
	SourceArchive   SourceKind = "archive"
	SourceCommand   SourceKind = "command"
)

// ErrUnsupportedSource is returned for a source kind that [Fetch] does not
// handle.
var ErrUnsupportedSource = errors.New("unsupported plugin source")

// Source is where a marketplace entry's plugin comes from.
type Source struct {
	Kind SourceKind
	// Path is the directory of a relative source, as written, or the
	// subdirectory of a git-subdir source.
	Path string
	// Repo is "owner/repo" for a github source.
	Repo string
	// URL is the git URL of a url or git-subdir source (git-subdir also
	// takes "owner/repo"), or the zip URL of an archive source.
	URL string
	// Ref is a branch or tag; SHA a full commit SHA that wins over Ref.
	Ref string
	SHA string

	Package  string // npm
	Version  string // npm
	Registry string // npm
	SHA256   string // archive
	Command  string // command
}

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// UnmarshalJSON reads a relative path string or a source object.
func (s *Source) UnmarshalJSON(b []byte) error {
	var path string
	if err := json.Unmarshal(b, &path); err == nil {
		*s = Source{Kind: SourceRelative, Path: path}
		return nil
	}
	var o struct {
		Source   string `json:"source"`
		Repo     string `json:"repo"`
		URL      string `json:"url"`
		Path     string `json:"path"`
		Ref      string `json:"ref"`
		SHA      string `json:"sha"`
		Package  string `json:"package"`
		Version  string `json:"version"`
		Registry string `json:"registry"`
		SHA256   string `json:"sha256"`
		Command  string `json:"command"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return fmt.Errorf("source: %w", err)
	}
	*s = Source{
		Kind: SourceKind(o.Source), Repo: o.Repo, URL: o.URL, Path: o.Path, Ref: o.Ref, SHA: o.SHA,
		Package: o.Package, Version: o.Version, Registry: o.Registry, SHA256: o.SHA256, Command: o.Command,
	}
	return nil
}

// MarshalJSON writes the source back in the marketplace form.
func (s Source) MarshalJSON() ([]byte, error) {
	if s.Kind == SourceRelative {
		return json.Marshal(s.Path)
	}
	o := map[string]string{"source": string(s.Kind)}
	for k, v := range map[string]string{
		"repo": s.Repo, "url": s.URL, "path": s.Path, "ref": s.Ref, "sha": s.SHA, "package": s.Package,
		"version": s.Version, "registry": s.Registry, "sha256": s.SHA256, "command": s.Command,
	} {
		if v != "" {
			o[k] = v
		}
	}
	return json.Marshal(o)
}

// Validate checks the fields of the source kind.
func (s Source) Validate() error {
	if s.SHA != "" && !fullSHA.MatchString(s.SHA) {
		return fmt.Errorf("sha must be a full 40-character lowercase commit SHA")
	}
	switch s.Kind {
	case SourceRelative:
		if strings.Contains(s.Path, `\`) {
			return fmt.Errorf("relative source %q: backslash in path", s.Path)
		}
		for _, seg := range strings.Split(s.Path, "/") {
			if seg == ".." {
				return fmt.Errorf("relative source %q: %w", s.Path, ErrPathTraversal)
			}
		}
	case SourceGitHub:
		if strings.Count(s.Repo, "/") != 1 {
			return fmt.Errorf("github source needs repo as owner/repo")
		}
	case SourceURL:
		if s.URL == "" {
			return fmt.Errorf("url source needs url")
		}
	case SourceGitSubdir:
		if s.URL == "" || s.Path == "" {
			return fmt.Errorf("git-subdir source needs url and path")
		}
		for _, seg := range strings.Split(s.Path, "/") {
			if seg == ".." {
				return fmt.Errorf("git-subdir path %q: %w", s.Path, ErrPathTraversal)
			}
		}
	case SourceNPM:
		if s.Package == "" {
			return fmt.Errorf("npm source needs package")
		}
	case SourceArchive:
		if s.URL == "" {
			return fmt.Errorf("archive source needs url")
		}
	case SourceCommand:
		if s.Command == "" {
			return fmt.Errorf("command source needs command")
		}
	default:
		return fmt.Errorf("unknown source type %q", s.Kind)
	}
	return nil
}

// Supported returns nil when [Fetch] can fetch the source, or an error
// wrapping [ErrUnsupportedSource] that names the kind.
func (s Source) Supported() error {
	switch s.Kind {
	case SourceRelative, SourceGitHub, SourceURL, SourceGitSubdir:
		return nil
	}
	return fmt.Errorf("%w %q", ErrUnsupportedSource, s.Kind)
}

// GitURL returns the clone URL of a git-based source: github repos and
// git-subdir "owner/repo" shorthands become https://github.com URLs.
func (s Source) GitURL() string {
	switch s.Kind {
	case SourceGitHub:
		return "https://github.com/" + s.Repo + ".git"
	case SourceURL:
		return s.URL
	case SourceGitSubdir:
		if isRepoShorthand(s.URL) {
			return "https://github.com/" + s.URL + ".git"
		}
		return s.URL
	}
	return ""
}

func isRepoShorthand(s string) bool {
	return !strings.Contains(s, ":") && strings.Count(s, "/") == 1 && !strings.HasPrefix(s, "/") && !strings.HasPrefix(s, ".")
}

// String describes the source for humans.
func (s Source) String() string {
	pin := ""
	switch {
	case s.SHA != "":
		pin = "@" + s.SHA[:12]
	case s.Ref != "":
		pin = "#" + s.Ref
	}
	switch s.Kind {
	case SourceRelative:
		return s.Path
	case SourceGitHub:
		return "github:" + s.Repo + pin
	case SourceGitSubdir:
		return s.URL + "//" + s.Path + pin
	case SourceNPM:
		return "npm:" + s.Package
	case SourceCommand:
		return "command"
	}
	return s.URL + pin
}

// Entry is one plugin of a marketplace.
type Entry struct {
	Name        string   `json:"name"`
	Source      Source   `json:"source"`
	Description string   `json:"description,omitempty"`
	Version     string   `json:"version,omitempty"`
	Category    string   `json:"category,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	DisplayName string   `json:"displayName,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	Author      *Person  `json:"-"`
	// Strict is nil when unset, which means true.
	Strict *bool `json:"strict,omitempty"`

	// Raw is the entry as written. Its plugin.json fields serve as the
	// manifest of a plugin that has none (see [LoadOptions].Entry).
	Raw json.RawMessage `json:"-"`
}

// Marketplace is a parsed marketplace.json.
type Marketplace struct {
	Name        string
	Owner       Person
	Description string
	Version     string
	// PluginRoot is metadata.pluginRoot: the directory bare relative
	// source names resolve under.
	PluginRoot string
	Plugins    []Entry
	// Renames maps a former plugin name to its current name, or to "" for
	// a removed plugin.
	Renames map[string]string

	// Root is the marketplace root on disk (the directory that contains
	// .claude-plugin/). Empty for a marketplace read from a URL, which
	// cannot resolve relative sources.
	Root string
	// SHA is the commit of the marketplace checkout, when known.
	SHA string
	// Skipped lists entries that failed to parse, with the reason.
	Skipped []Skipped
}

// ParseMarketplace decodes a marketplace.json document. An invalid entry is
// listed in Skipped and does not fail the marketplace.
func ParseMarketplace(data []byte) (*Marketplace, error) {
	var doc struct {
		Name        string            `json:"name"`
		Owner       json.RawMessage   `json:"owner"`
		Description string            `json:"description"`
		Version     flexString        `json:"version"`
		Plugins     []json.RawMessage `json:"plugins"`
		Renames     map[string]*string
		Metadata    struct {
			Description string     `json:"description"`
			Version     flexString `json:"version"`
			PluginRoot  string     `json:"pluginRoot"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("marketplace.json: %w", err)
	}
	if err := ValidateMarketplaceName(doc.Name); err != nil {
		return nil, err
	}
	m := &Marketplace{
		Name:        doc.Name,
		Description: doc.Description,
		Version:     string(doc.Version),
		PluginRoot:  doc.Metadata.PluginRoot,
	}
	if p := decodePerson(doc.Owner); p != nil {
		m.Owner = *p
	}
	if m.Description == "" {
		m.Description = doc.Metadata.Description
	}
	if m.Version == "" {
		m.Version = string(doc.Metadata.Version)
	}
	if len(doc.Renames) > 0 {
		m.Renames = map[string]string{}
		for k, v := range doc.Renames {
			if v != nil {
				m.Renames[k] = *v
			} else {
				m.Renames[k] = ""
			}
		}
	}
	seen := map[string]bool{}
	for i, raw := range doc.Plugins {
		var e Entry
		if err := json.Unmarshal(raw, &e); err != nil {
			m.Skipped = append(m.Skipped, Skipped{Component: "entry", Name: fmt.Sprintf("plugins[%d]", i), Reason: err.Error()})
			continue
		}
		var a struct {
			Author json.RawMessage `json:"author"`
		}
		_ = json.Unmarshal(raw, &a)
		e.Author = decodePerson(a.Author)
		e.Raw = raw
		if err := ValidateMarketplaceName(e.Name); err != nil {
			m.Skipped = append(m.Skipped, Skipped{Component: "entry", Name: fmt.Sprintf("plugins[%d]", i), Reason: err.Error()})
			continue
		}
		if err := e.Source.Validate(); err != nil {
			m.Skipped = append(m.Skipped, Skipped{Component: "entry", Name: e.Name, Reason: err.Error()})
			continue
		}
		if seen[e.Name] {
			m.Skipped = append(m.Skipped, Skipped{Component: "entry", Name: e.Name, Reason: "duplicate plugin name"})
			continue
		}
		seen[e.Name] = true
		m.Plugins = append(m.Plugins, e)
	}
	return m, nil
}

var marketplaceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidateMarketplaceName checks a marketplace or entry name: letters,
// digits, '.', '_' and '-', starting with a letter or digit, without "..".
func ValidateMarketplaceName(name string) error {
	if !marketplaceName.MatchString(name) || strings.Contains(name, "..") {
		return fmt.Errorf("invalid name %q: use letters, digits, '.', '_' and '-', starting with a letter or digit", name)
	}
	return nil
}

// LoadMarketplace reads <dir>/.claude-plugin/marketplace.json and sets Root
// to dir and SHA to the checkout's commit when dir is a git work tree.
func LoadMarketplace(dir string) (*Marketplace, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(MarketplacePath)))
	if err != nil {
		return nil, err
	}
	m, err := ParseMarketplace(data)
	if err != nil {
		return nil, err
	}
	m.Root = root
	m.SHA = gitHead(root)
	return m, nil
}

// IsMarketplace reports whether dir holds a marketplace file.
func IsMarketplace(dir string) bool {
	return isFile(filepath.Join(dir, filepath.FromSlash(MarketplacePath)))
}

// Find returns the entry named name, following Renames.
func (m *Marketplace) Find(name string) (*Entry, error) {
	for range 8 {
		for i := range m.Plugins {
			if m.Plugins[i].Name == name {
				return &m.Plugins[i], nil
			}
		}
		to, ok := m.Renames[name]
		if !ok {
			break
		}
		if to == "" {
			return nil, fmt.Errorf("plugin %q was removed from marketplace %q", name, m.Name)
		}
		name = to
	}
	return nil, fmt.Errorf("plugin %q not found in marketplace %q", name, m.Name)
}

// RelativeDir returns the directory of a relative source under the
// marketplace root, applying metadata.pluginRoot to a bare name.
func (m *Marketplace) RelativeDir(s Source) (string, error) {
	if s.Kind != SourceRelative {
		return "", fmt.Errorf("not a relative source")
	}
	if m.Root == "" {
		return "", fmt.Errorf("marketplace %q has no local files; relative sources need a git or directory marketplace", m.Name)
	}
	p := s.Path
	if p != "." && p != "./" && !strings.HasPrefix(p, "./") {
		if m.PluginRoot == "" || strings.Contains(p, "/") {
			return "", fmt.Errorf("relative source %q: %w", p, ErrPathPrefix)
		}
		root := m.PluginRoot
		if !strings.HasPrefix(root, "./") {
			root = "./" + root
		}
		p = strings.TrimSuffix(root, "/") + "/" + p
	}
	return CheckPath(m.Root, p, true)
}

// ParsePluginID splits "name@marketplace". ok is false when id has no '@' or
// either part is not a valid name.
func ParsePluginID(id string) (name, marketplace string, ok bool) {
	name, marketplace, found := strings.Cut(id, "@")
	if !found || ValidateMarketplaceName(name) != nil || ValidateMarketplaceName(marketplace) != nil {
		return "", "", false
	}
	return name, marketplace, true
}
