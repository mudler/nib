package skill

import (
	"path/filepath"
)

// SkillsDir is where installed skill-pack checkouts live, parallel to plugins.
func SkillsDir(baseDir string) string { return filepath.Join(baseDir, "skills") }

func packDir(baseDir, name string) string { return filepath.Join(SkillsDir(baseDir), name) }

func registryPath(baseDir string) string { return filepath.Join(baseDir, "skills.yaml") }
