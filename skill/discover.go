package skill

import (
	"fmt"
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

// pack is an enabled skill pack: its registry name and on-disk directory.
type pack struct {
	name string
	dir  string
}

// enabledPacks returns the enabled packs from the registry, in registry order.
func (mgr *Manager) enabledPacks() []pack {
	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wiz: skill registry: %v\n", err)
		return nil
	}
	var out []pack
	for _, e := range reg.Entries {
		if !e.Enabled {
			continue
		}
		out = append(out, pack{name: e.Name, dir: packDir(mgr.baseDir, e.Name)})
	}
	return out
}

// Apply merges every enabled pack's skills into cfg. Precedence is packs <
// user: a user skill of the same name wins; a pack-vs-pack clash is last-wins
// with a warning. Call this BEFORE plugin.Apply so packs also win over plugin
// skills (plugin.Apply skips names already present in cfg.Skills).
func Apply(cfg *types.Config, baseDir string) error {
	mergeSkills(cfg, NewManager(baseDir).enabledPacks())
	return nil
}

func mergeSkills(cfg *types.Config, packs []pack) {
	userSkills := map[string]bool{}
	for _, s := range cfg.Skills {
		userSkills[s.Name] = true
	}
	order := []string{}
	byName := map[string]types.Skill{}
	from := map[string]string{}

	for _, p := range packs {
		skills, err := HarvestPack(p.dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "wiz: skill pack %q: %v\n", p.name, err)
			continue
		}
		for _, s := range skills {
			if userSkills[s.Name] {
				continue // user wins
			}
			if _, ok := byName[s.Name]; ok {
				fmt.Fprintf(os.Stderr, "wiz: skill %q from pack %q overrides pack %q\n", s.Name, p.name, from[s.Name])
			} else {
				order = append(order, s.Name)
			}
			byName[s.Name] = s
			from[s.Name] = p.name
		}
	}
	for _, name := range order {
		cfg.Skills = append(cfg.Skills, byName[name])
	}
}
