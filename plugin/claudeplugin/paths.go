package claudeplugin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrPathPrefix is returned for a component path that does not start with
// "./".
var ErrPathPrefix = errors.New(`path must start with "./"`)

// ErrPathTraversal is returned for a path that contains a ".." segment or
// resolves outside its root.
var ErrPathTraversal = errors.New(`path contains ".." or escapes the plugin directory`)

// CheckPath validates a component path from a manifest or a marketplace entry
// and returns its absolute form under root. The path must start with "./"
// (allowDot also accepts "." alone), must not contain a ".." segment or a
// backslash, and must resolve inside root after following symlinks that exist.
func CheckPath(root, p string, allowDot bool) (string, error) {
	if allowDot && (p == "." || p == "./") {
		return filepath.Clean(root), nil
	}
	if !strings.HasPrefix(p, "./") {
		return "", fmt.Errorf("%q: %w", p, ErrPathPrefix)
	}
	if strings.Contains(p, `\`) {
		return "", fmt.Errorf("%q: backslash in path", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", fmt.Errorf("%q: %w", p, ErrPathTraversal)
		}
	}
	return within(root, filepath.Join(root, filepath.FromSlash(p)))
}

// within returns full when it stays inside root, resolving symlinks of the
// parts that exist so a link cannot point outside.
func within(root, full string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	full = filepath.Clean(full)
	if !inside(rootAbs, full) {
		return "", fmt.Errorf("%q: %w", full, ErrPathTraversal)
	}
	realRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return full, nil
	}
	if realFull, err := filepath.EvalSymlinks(full); err == nil && !inside(realRoot, realFull) {
		return "", fmt.Errorf("%q: %w", full, ErrPathTraversal)
	}
	return full, nil
}

func inside(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(os.PathSeparator))
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}
