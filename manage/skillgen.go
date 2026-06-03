package manage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/nib/skill"

	"gopkg.in/yaml.v3"
)

// validSkillName reports whether name is a single safe path segment.
func validSkillName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

// GenerateSkill writes a new SKILL.md into the shared "local" skill pack and
// ensures that pack is registered enabled, so the skill becomes loadable once
// the session reloads. Returns the new skill's tool-facing info.
func (c *Configurator) GenerateSkill(name, description, instructions string) (SkillInfo, error) {
	if !validSkillName(name) {
		return SkillInfo{}, fmt.Errorf("invalid skill name %q (must be a single path segment)", name)
	}
	dir := filepath.Join(skill.SkillsDir(c.baseDir), "local", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return SkillInfo{}, err
	}
	fm, err := yaml.Marshal(map[string]string{"name": name, "description": description})
	if err != nil {
		return SkillInfo{}, err
	}
	content := "---\n" + string(fm) + "---\n\n" + strings.TrimRight(instructions, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		return SkillInfo{}, err
	}

	reg, err := skill.LoadRegistry(c.baseDir)
	if err != nil {
		return SkillInfo{}, err
	}
	if reg.Find("local") == nil {
		reg.Upsert(skill.Entry{Name: "local", SourceURL: "(generated)", Enabled: true})
		if err := reg.Save(); err != nil {
			return SkillInfo{}, err
		}
	}
	return SkillInfo{Name: name, Description: description, Pack: "local"}, nil
}
