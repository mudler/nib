// Package extsource resolves non-git extension sources (zip archives, bare
// SKILL.md URLs) into a local directory that skill/plugin managers can install.
package extsource

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ExtractZip unzips zipPath into the existing directory destDir. It rejects any
// entry whose path escapes destDir (zip-slip), any absolute path, and any
// symlink entry, so a crafted archive cannot write outside destDir.
func ExtractZip(zipPath, destDir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	root, err := filepath.Abs(destDir)
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("zip contains a symlink entry %q; refusing", f.Name)
		}
		if filepath.IsAbs(f.Name) {
			return fmt.Errorf("zip contains an absolute path %q; refusing", f.Name)
		}
		target := filepath.Join(root, f.Name)
		// The cleaned target must stay within root (guard against ../ escapes).
		if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
			return fmt.Errorf("zip entry %q escapes destination", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := writeZipFile(f, target); err != nil {
			return err
		}
	}
	return nil
}

// FetchSKILLURL downloads a single SKILL.md from url into destDir/SKILL.md. It
// is the agentskills.io "bare skill file" form; the caller installs destDir as
// a one-skill pack.
func FetchSKILLURL(url, destDir string) error {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: unexpected status %d", url, resp.StatusCode)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	out, err := os.Create(filepath.Join(destDir, "SKILL.md"))
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, io.LimitReader(resp.Body, 5<<20)); err != nil {
		return err
	}
	return nil
}

func writeZipFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, f.Mode().Perm()|0o200)
	if err != nil {
		return err
	}
	defer out.Close()
	// Bound the copy to guard against decompression bombs on untrusted input.
	if _, err := io.Copy(out, io.LimitReader(rc, 200<<20)); err != nil {
		return err
	}
	return nil
}
