package skill

import (
	"os"
	"path/filepath"

	"github.com/mudler/wiz/plugin"
	"github.com/mudler/wiz/types"
)

// HarvestPack reads skills/<name>/SKILL.md from a skill-pack root and returns
// the contributed skills, each with Dir set to its on-disk directory so the
// load_skill tool can point the agent at bundled scripts and references. A
// missing skills/ directory yields no skills and no error.
func HarvestPack(root string) ([]types.Skill, error) {
	skillsRoot := filepath.Join(root, "skills")
	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []types.Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(skillsRoot, e.Name())
		data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			continue // a subdir without SKILL.md is not a skill
		}
		name, desc, tools, body := plugin.ParseSkillMarkdown(data)
		if name == "" {
			name = e.Name()
		}
		out = append(out, types.Skill{
			Name:         name,
			Description:  desc,
			Instructions: body,
			Tools:        tools,
			Dir:          dir,
		})
	}
	return out, nil
}
