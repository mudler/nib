package claudeplugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitFunc runs git with args in dir and returns its stdout. Hosts and tests
// may replace [DefaultGit].
type GitFunc func(ctx context.Context, dir string, args ...string) (string, error)

// DefaultGit runs the git binary.
var DefaultGit GitFunc = func(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// FetchOptions tunes [Fetch] and [FetchMarketplace].
type FetchOptions struct {
	// SHA pins the checkout to this commit. It wins over the source's own
	// sha and ref. For a relative source it is checked against the
	// marketplace commit.
	SHA string
	// Ref overrides the source's ref when SHA is empty.
	Ref string
	// Git runs git. Nil uses DefaultGit.
	Git GitFunc
	// HTTPClient fetches a marketplace.json by URL. Nil uses
	// http.DefaultClient.
	HTTPClient *http.Client
	// MaxBytes and MaxFiles cap the size of the copied plugin or
	// marketplace (regular files only). 0 means no cap. A fetch over a cap
	// fails with ErrTooLarge and leaves nothing at dest. The context
	// deadline bounds the whole fetch, git included.
	MaxBytes int64
	MaxFiles int
}

// ErrTooLarge is returned when a fetch exceeds FetchOptions.MaxBytes or
// MaxFiles.
var ErrTooLarge = errors.New("fetch exceeds the size limit")

func (o FetchOptions) limits() copyLimits {
	return copyLimits{maxBytes: o.MaxBytes, maxFiles: o.MaxFiles}
}

// copyLimitsAndClean copies src to dst under the limits and removes dst on
// failure.
func copyLimited(src, dst string, lim copyLimits) error {
	if err := copyTree(src, dst, lim); err != nil {
		_ = os.RemoveAll(dst)
		return err
	}
	return nil
}

func (o FetchOptions) git() GitFunc {
	if o.Git != nil {
		return o.Git
	}
	return DefaultGit
}

// FetchResult describes a fetched plugin.
type FetchResult struct {
	// Dir is the plugin directory (the dest passed to Fetch).
	Dir string
	// SHA is the commit the files came from: the plugin repository's for
	// a git source, the marketplace's for a relative source. Empty when
	// the marketplace is not a git checkout.
	SHA    string
	Source Source
}

// Fetch copies the plugin of entry e into dest, which must not exist. A git
// source is checked out at opts.SHA, the source sha, opts.Ref or the source
// ref, in that order; the commit used is returned so a host can pin it. A
// relative source is copied from the marketplace root. Other source kinds
// return an error wrapping [ErrUnsupportedSource]. Temporary clones are made
// next to dest and removed.
func Fetch(ctx context.Context, m *Marketplace, e *Entry, dest string, opts FetchOptions) (*FetchResult, error) {
	if err := e.Source.Supported(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(dest); err == nil {
		return nil, fmt.Errorf("fetch: %s already exists", dest)
	}
	src := e.Source
	res := &FetchResult{Dir: dest, Source: src}
	if src.Kind == SourceRelative {
		dir, err := m.RelativeDir(src)
		if err != nil {
			return nil, err
		}
		if !isDir(dir) {
			return nil, fmt.Errorf("source path does not exist: %s", src.Path)
		}
		if opts.SHA != "" && m.SHA != "" && opts.SHA != m.SHA {
			return nil, fmt.Errorf("marketplace %q is at %s, not at the pinned %s; update the marketplace first", m.Name, m.SHA, opts.SHA)
		}
		if err := copyLimited(dir, dest, opts.limits()); err != nil {
			return nil, err
		}
		res.SHA = m.SHA
		return res, nil
	}

	sha := firstNonEmpty(opts.SHA, src.SHA)
	ref := firstNonEmpty(opts.Ref, src.Ref)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dest), ".fetch-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	var sparse string
	if src.Kind == SourceGitSubdir {
		sparse = strings.Trim(src.Path, "/")
	}
	head, err := checkout(ctx, opts.git(), src.GitURL(), ref, sha, sparse, tmp)
	if err != nil {
		return nil, err
	}
	dir := tmp
	if sparse != "" {
		dir, err = CheckPath(tmp, "./"+strings.TrimPrefix(sparse, "./"), false)
		if err != nil {
			return nil, err
		}
		if !isDir(dir) {
			return nil, fmt.Errorf("path %q not found in %s", src.Path, src.GitURL())
		}
	}
	if err := copyLimited(dir, dest, opts.limits()); err != nil {
		return nil, err
	}
	res.SHA = head
	return res, nil
}

// checkout makes a shallow checkout of url at sha, ref or the default
// branch into dir and returns the commit. With sparse set, only that
// subdirectory is checked out.
func checkout(ctx context.Context, git GitFunc, url, ref, sha, sparse, dir string) (string, error) {
	if _, err := git(ctx, dir, "init", "-q"); err != nil {
		return "", err
	}
	if _, err := git(ctx, dir, "remote", "add", "origin", url); err != nil {
		return "", err
	}
	if sparse != "" {
		if _, err := git(ctx, dir, "sparse-checkout", "set", "--no-cone", "/"+sparse+"/"); err != nil {
			return "", err
		}
	}
	want := firstNonEmpty(sha, ref, "HEAD")
	if _, err := git(ctx, dir, "fetch", "-q", "--depth", "1", "origin", want); err != nil {
		if sha == "" {
			return "", err
		}
		// Some servers refuse a fetch by SHA; fetch the history instead.
		if _, err2 := git(ctx, dir, "fetch", "-q", "origin", firstNonEmpty(ref, "HEAD")); err2 != nil {
			return "", errors.Join(err, err2)
		}
		want = sha
	} else if sha == "" {
		want = "FETCH_HEAD"
	}
	if _, err := git(ctx, dir, "checkout", "-q", "--detach", want); err != nil {
		if want != "FETCH_HEAD" {
			if _, err2 := git(ctx, dir, "checkout", "-q", "--detach", "FETCH_HEAD"); err2 != nil {
				return "", err
			}
		} else {
			return "", err
		}
	}
	head, err := git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if sha != "" && head != sha {
		return "", fmt.Errorf("checked out %s, want pinned %s", head, sha)
	}
	return head, nil
}

// MarketplaceSource is where a marketplace comes from, parsed from the
// string a user types: a local directory, "owner/repo" (optionally with
// "@ref" or "#ref"), a git URL (optionally with "#ref"), or an http(s) URL
// to a marketplace.json file.
type MarketplaceSource struct {
	// Kind is "directory", "github", "git" or "url".
	Kind string
	Path string // directory
	Repo string // github
	URL  string // git, url
	Ref  string
}

// ParseMarketplaceSource parses a marketplace location.
func ParseMarketplaceSource(s string) (MarketplaceSource, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return MarketplaceSource{}, fmt.Errorf("empty marketplace source")
	}
	if isDir(s) {
		abs, err := filepath.Abs(s)
		return MarketplaceSource{Kind: "directory", Path: abs}, err
	}
	base, ref := s, ""
	if i := strings.LastIndex(s, "#"); i > 0 {
		base, ref = s[:i], s[i+1:]
	}
	switch {
	case strings.HasPrefix(base, "git@") || strings.HasPrefix(base, "file://") || strings.HasPrefix(base, "ssh://"):
		return MarketplaceSource{Kind: "git", URL: base, Ref: ref}, nil
	case strings.HasPrefix(base, "http://") || strings.HasPrefix(base, "https://"):
		if strings.HasSuffix(base, ".json") {
			return MarketplaceSource{Kind: "url", URL: base}, nil
		}
		return MarketplaceSource{Kind: "git", URL: base, Ref: ref}, nil
	}
	if i := strings.LastIndex(base, "@"); i > 0 && ref == "" {
		base, ref = base[:i], base[i+1:]
	}
	if isRepoShorthand(base) {
		return MarketplaceSource{Kind: "github", Repo: base, Ref: ref}, nil
	}
	return MarketplaceSource{}, fmt.Errorf("marketplace source %q: not a directory, owner/repo, git URL or marketplace.json URL", s)
}

// String gives back the source in the form ParseMarketplaceSource reads.
func (s MarketplaceSource) String() string {
	ref := ""
	if s.Ref != "" {
		ref = "#" + s.Ref
	}
	switch s.Kind {
	case "directory":
		return s.Path
	case "github":
		return s.Repo + ref
	case "git":
		return s.URL + ref
	}
	return s.URL
}

// FetchMarketplace fetches a marketplace into dest, which must not exist,
// and loads it. A git or github source is checked out (at opts.SHA when set)
// and keeps no .git directory; the commit is in Marketplace.SHA. A directory
// is copied, and a URL source downloads only marketplace.json, so its
// relative sources cannot be installed.
func FetchMarketplace(ctx context.Context, src MarketplaceSource, dest string, opts FetchOptions) (*Marketplace, error) {
	if _, err := os.Stat(dest); err == nil {
		return nil, fmt.Errorf("fetch: %s already exists", dest)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, err
	}
	switch src.Kind {
	case "directory":
		if !IsMarketplace(src.Path) {
			return nil, fmt.Errorf("%s: no %s", src.Path, MarketplacePath)
		}
		sha := gitHead(src.Path)
		if err := copyLimited(src.Path, dest, opts.limits()); err != nil {
			return nil, err
		}
		m, err := LoadMarketplace(dest)
		if err != nil {
			return nil, err
		}
		m.SHA = sha
		return m, nil
	case "github", "git":
		url := src.URL
		if src.Kind == "github" {
			url = "https://github.com/" + src.Repo + ".git"
		}
		tmp, err := os.MkdirTemp(filepath.Dir(dest), ".fetch-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmp)
		head, err := checkout(ctx, opts.git(), url, firstNonEmpty(opts.Ref, src.Ref), opts.SHA, "", tmp)
		if err != nil {
			return nil, err
		}
		if !IsMarketplace(tmp) {
			return nil, fmt.Errorf("%s: no %s", url, MarketplacePath)
		}
		if err := copyLimited(tmp, dest, opts.limits()); err != nil {
			return nil, err
		}
		m, err := LoadMarketplace(dest)
		if err != nil {
			return nil, err
		}
		m.SHA = head
		return m, nil
	case "url":
		client := opts.HTTPClient
		if client == nil {
			client = http.DefaultClient
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GET %s: %s", src.URL, resp.Status)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if err != nil {
			return nil, err
		}
		m, err := ParseMarketplace(data)
		if err != nil {
			return nil, err
		}
		file := filepath.Join(dest, filepath.FromSlash(MarketplacePath))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(file, data, 0o644); err != nil {
			return nil, err
		}
		return m, nil
	}
	return nil, fmt.Errorf("unknown marketplace source kind %q", src.Kind)
}

// CopyTree copies the files under src into dst (created), skipping .git.
// File modes are kept so scripts stay executable. A symlink is copied only
// when its target stays inside src; others are skipped.
func CopyTree(src, dst string) error { return copyTree(src, dst, copyLimits{}) }

type copyLimits struct {
	maxBytes int64
	maxFiles int
}

func copyTree(src, dst string, lim copyLimits) error {
	var bytes int64
	var files int
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	realSrc, err := filepath.EvalSymlinks(srcAbs)
	if err != nil {
		return err
	}
	return filepath.WalkDir(realSrc, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(realSrc, p)
		if err != nil {
			return err
		}
		if d.Name() == ".git" && p != realSrc {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return nil
			}
			resolved := link
			if !filepath.IsAbs(link) {
				resolved = filepath.Join(filepath.Dir(p), link)
			}
			if real, err := filepath.EvalSymlinks(resolved); err != nil || !inside(realSrc, real) || filepath.IsAbs(link) {
				return nil
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			files++
			bytes += info.Size()
			if lim.maxFiles > 0 && files > lim.maxFiles || lim.maxBytes > 0 && bytes > lim.maxBytes {
				return fmt.Errorf("%w (%d files, %d bytes so far)", ErrTooLarge, files, bytes)
			}
			return copyFile(p, target, info.Mode().Perm())
		}
		return nil
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// gitHead returns the commit of a git work tree, or "".
func gitHead(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return ""
	}
	out, err := DefaultGit(context.Background(), dir, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ResolveRef returns the commit a git-based source points at without a
// checkout, for update checks: the source's sha when set, otherwise the
// commit of ref (or of the source's ref, or of the default branch) from
// "git ls-remote". An annotated tag resolves to the commit it tags. A
// relative source has no commit of its own (it is the marketplace's), and
// other kinds are unsupported.
func ResolveRef(ctx context.Context, src Source, ref string, opts FetchOptions) (string, error) {
	switch src.Kind {
	case SourceGitHub, SourceURL, SourceGitSubdir:
	case SourceRelative:
		return "", fmt.Errorf("a relative source has the marketplace's commit")
	default:
		return "", fmt.Errorf("%w %q", ErrUnsupportedSource, src.Kind)
	}
	if ref == "" && src.SHA != "" {
		return src.SHA, nil
	}
	ref = firstNonEmpty(ref, src.Ref)
	if fullSHA.MatchString(ref) {
		return ref, nil
	}
	pattern := firstNonEmpty(ref, "HEAD")
	args := []string{"ls-remote", "--", src.GitURL(), pattern}
	if ref != "" {
		args = append(args, ref+"^{}") // the commit of an annotated tag
	}
	out, err := opts.git()(ctx, "", args...)
	if err != nil {
		return "", err
	}
	var direct, peeled string
	for _, line := range strings.Split(out, "\n") {
		sha, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || !fullSHA.MatchString(sha) {
			continue
		}
		switch {
		case strings.HasSuffix(name, "^{}"):
			if peeled == "" {
				peeled = sha
			}
		case direct == "":
			direct = sha
		}
	}
	if peeled != "" {
		return peeled, nil
	}
	if direct == "" {
		return "", fmt.Errorf("ref %q not found in %s", pattern, src.GitURL())
	}
	return direct, nil
}
