package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/wiz/types"
)

// resolveFragment returns a fragment's text: inline Text when set, otherwise the
// contents of File read relative to the plugin root.
func resolveFragment(f FragmentSpec, root string) (string, error) {
	if strings.TrimSpace(f.Text) != "" {
		return f.Text, nil
	}
	if f.File == "" {
		return "", fmt.Errorf("prompt fragment has neither text nor file")
	}
	b, err := readPluginFile(root, f.File)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// resolveSkill converts a manifest SkillSpec into a runtime types.Skill, loading
// the instructions body (inline, or from a file relative to the plugin root).
func resolveSkill(s SkillSpec, root string) (types.Skill, error) {
	body := s.Instructions.Inline
	if strings.TrimSpace(body) == "" {
		if s.Instructions.File == "" {
			return types.Skill{}, fmt.Errorf("skill %q has no instructions", s.Name)
		}
		b, err := readPluginFile(root, s.Instructions.File)
		if err != nil {
			return types.Skill{}, err
		}
		body = string(b)
	}
	return types.Skill{
		Name:         s.Name,
		Description:  s.Description,
		Instructions: body,
		Tools:        s.Tools,
	}, nil
}

// readPluginFile reads a path relative to a plugin root, refusing paths that
// escape the root (defense in depth against ../ traversal in a manifest).
func readPluginFile(root, rel string) ([]byte, error) {
	rootAbs := filepath.Clean(root)
	full := filepath.Clean(filepath.Join(rootAbs, rel))
	if full != rootAbs && !strings.HasPrefix(full, rootAbs+string(os.PathSeparator)) {
		return nil, fmt.Errorf("file %q escapes plugin directory", rel)
	}
	return os.ReadFile(full)
}
