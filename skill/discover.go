package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/nib/plugin"
	"github.com/mudler/nib/types"
)

// HarvestPack walks a skill-pack root and returns every contributed skill.
// Discovery is recursive: any directory containing a SKILL.md is a skill, its
// Name taken from the file's frontmatter (falling back to the directory name)
// and its Dir set to that directory so the load_skill tool can resolve bundled
// scripts and references. Once a directory is recognized as a skill its subtree
// is pruned — a skill's own references/examples cannot define further skills.
// Dotted directories (e.g. .git) and nested symlinks are skipped, and a
// SKILL.md sitting directly at root is not itself a skill. A missing or
// unreadable directory yields no skills and no error.
func HarvestPack(root string) ([]types.Skill, error) {
	var out []types.Skill
	var walk func(dir string)
	walk = func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return // unreadable subtree: skip, don't fail the whole harvest
		}
		for _, e := range entries {
			// e.IsDir() is false for symlinks (DirEntry reports the link's own
			// type), so this also skips nested symlinks — avoiding cycles.
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			child := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(filepath.Join(child, "SKILL.md"))
			if err != nil {
				walk(child) // not a skill dir → descend
				continue
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
				Dir:          child,
			})
			// prune: do not descend into a recognized skill dir
		}
	}
	walk(root)
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
		fmt.Fprintf(os.Stderr, "nib: skill registry: %v\n", err)
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
			fmt.Fprintf(os.Stderr, "nib: skill pack %q: %v\n", p.name, err)
			continue
		}
		for _, s := range skills {
			if userSkills[s.Name] {
				continue // user wins
			}
			if _, ok := byName[s.Name]; ok {
				fmt.Fprintf(os.Stderr, "nib: skill %q from pack %q overrides pack %q\n", s.Name, p.name, from[s.Name])
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
