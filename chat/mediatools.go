package chat

import "path/filepath"

// resolveWorkspacePath joins a relative path onto workingDir (absolute paths are
// used as-is, and an empty workingDir leaves the path unchanged), matching how
// host file tools scope reads.
func resolveWorkspacePath(workingDir, p string) string {
	if workingDir == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(workingDir, p)
}
