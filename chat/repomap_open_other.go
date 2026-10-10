//go:build !unix

package chat

import (
	"errors"
	"os"
)

// Fail closed where descriptor-relative no-follow opening is unavailable.
func openRepoMapRegular(string) (*os.File, error) {
	return nil, errors.New("repo_map safe file opening is unsupported on this platform")
}
func openRepoMapDirectory(string) (*os.File, error) {
	return nil, errors.New("repo_map safe directory opening is unsupported on this platform")
}
