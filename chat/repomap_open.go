//go:build unix

package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Walk descriptors from / with O_NOFOLLOW at every component. This excludes
// symlinks even when a directory is replaced concurrently during discovery.
func openRepoMapPath(path string, directory bool) (*os.File, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, p := range parts {
		if p == "" {
			continue
		}
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
		if i < len(parts)-1 || directory {
			flags |= unix.O_DIRECTORY
		}
		var stat unix.Stat_t
		if e := unix.Fstatat(fd, p, &stat, unix.AT_SYMLINK_NOFOLLOW); e != nil {
			unix.Close(fd)
			return nil, e
		}
		kind := stat.Mode & unix.S_IFMT
		if kind != unix.S_IFDIR && (i < len(parts)-1 || directory || kind != unix.S_IFREG) {
			unix.Close(fd)
			return nil, fmt.Errorf("not regular: %s", path)
		}
		next, e := unix.Openat(fd, p, flags, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	f := os.NewFile(uintptr(fd), path)
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if (!directory && !fi.Mode().IsRegular()) || (directory && !fi.IsDir()) {
		f.Close()
		return nil, fmt.Errorf("not a regular file/directory: %s", path)
	}
	return f, nil
}
func openRepoMapRegular(path string) (*os.File, error)   { return openRepoMapPath(path, false) }
func openRepoMapDirectory(path string) (*os.File, error) { return openRepoMapPath(path, true) }
