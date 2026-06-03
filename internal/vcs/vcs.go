// Package vcs holds the git/copy helpers shared by the plugin and skill
// installers. The functions are package-level vars so tests can inject fakes.
package vcs

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Clone clones url (optionally at ref) into dest. Var for test injection.
var Clone = func(url, ref, dest string) error {
	args := []string{"clone", "--depth", "1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, url, dest)
	cmd := exec.Command("git", args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Pull fast-forwards an existing checkout. Var for test injection.
var Pull = func(dir string) error {
	cmd := exec.Command("git", "-C", dir, "pull", "--ff-only")
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// CopyDir recursively copies the contents of src into dst (which must already
// exist), skipping any .git directory and preserving file permission bits (so
// hook/skill scripts stay executable). Used to install from a local directory
// without requiring it to be a git repository.
func CopyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if info, e := d.Info(); e == nil {
			mode = info.Mode().Perm()
		}
		return os.WriteFile(filepath.Join(dst, rel), data, mode)
	})
}
