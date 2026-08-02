package catalog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/nib/extsource"
	"github.com/mudler/nib/internal/vcs"
	"github.com/mudler/nib/plugin"
	"github.com/mudler/nib/skill"
)

// Install materializes m into a local directory and installs it through the
// matching manager, DISABLED. A git locator clones the repo (Ref optional) and
// installs the Path subdirectory; a ZipURL downloads and extracts the archive.
// The extension's real origin — not the throwaway extraction dir — is recorded
// as its registry provenance. It returns the installed extension's name.
func (c *Client) Install(ctx context.Context, m Meta, baseDir, nibVersion string) (string, error) {
	dir, cleanup, err := c.materialize(ctx, m)
	if err != nil {
		return "", err
	}
	defer cleanup()

	source := m.sourceLocator()
	switch m.Kind {
	case KindSkill:
		name, _, err := skill.NewManagerFor(baseDir, c.ProgramName).InstallDir(dir, m.Name, source)
		return name, err
	case KindPlugin:
		mani, err := plugin.NewManager(baseDir).InstallDir(dir, nibVersion, source)
		if err != nil {
			return "", err
		}
		return mani.Name, nil
	default:
		return "", fmt.Errorf("unknown extension kind %q", m.Kind)
	}
}

// sourceLocator returns a stable, human-meaningful identifier for m's origin,
// recorded as registry provenance so `list` shows the real source rather than
// the temp extraction dir. Priority mirrors materialize: ZipURL, then GitURL,
// then the owner/repo(+subpath) form.
func (m Meta) sourceLocator() string {
	switch {
	case m.ZipURL != "":
		return m.ZipURL
	case m.GitURL != "":
		if m.Path != "" {
			return strings.TrimSuffix(m.GitURL, "/") + "/" + strings.TrimPrefix(m.Path, "/")
		}
		return m.GitURL
	case m.Repo != "":
		if m.Path != "" {
			return strings.TrimSuffix(m.Repo, "/") + "/" + strings.TrimPrefix(m.Path, "/")
		}
		return m.Repo
	default:
		return ""
	}
}

// materialize resolves m's install locator into a local directory, returning a
// cleanup func the caller must always call. A ZipURL is downloaded and
// extracted (zip-slip-guarded by extsource); a GitURL/Repo is cloned and, if
// Path is set, the cleaned subdirectory is returned (Path cannot escape the
// checkout).
func (c *Client) materialize(ctx context.Context, m Meta) (dir string, cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", "nib-catalog-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup = func() { os.RemoveAll(tmp) }
	fail := func(err error) (string, func(), error) {
		cleanup()
		return "", func() {}, err
	}

	switch {
	case m.ZipURL != "":
		zipPath := filepath.Join(tmp, "ext.zip")
		if err := c.download(ctx, m.ZipURL, zipPath); err != nil {
			return fail(err)
		}
		out := filepath.Join(tmp, "unzipped")
		if err := os.MkdirAll(out, 0o755); err != nil {
			return fail(err)
		}
		if err := extsource.ExtractZip(zipPath, out); err != nil {
			return fail(err)
		}
		return out, cleanup, nil

	case m.GitURL != "" || m.Repo != "":
		url := m.GitURL
		if url == "" {
			url = "https://github.com/" + m.Repo
		}
		checkout := filepath.Join(tmp, "repo")
		if err := vcs.Clone(url, m.Ref, checkout); err != nil {
			return fail(fmt.Errorf("clone %s: %w", url, err))
		}
		sub := checkout
		if m.Path != "" {
			// Clean("/"+Path) keeps the subpath rooted so a "../" cannot escape.
			sub = filepath.Join(checkout, filepath.Clean("/"+m.Path))
		}
		return sub, cleanup, nil

	default:
		return fail(fmt.Errorf("meta %q has no install locator (need gitURL, repo, or zipURL)", m.Name))
	}
}

// download GETs url into dst with a bounded body.
func (c *Client) download(ctx context.Context, url, dst string) error {
	data, err := c.getBytes(ctx, url, 200<<20)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
