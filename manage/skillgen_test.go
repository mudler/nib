package manage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/nib/skill"
)

func TestGenerateSkillWritesAndRegisters(t *testing.T) {
	c, base := newTestConfigurator(t)
	info, err := c.GenerateSkill("greet", "Greet the user", "Say hello politely.")
	if err != nil {
		t.Fatalf("GenerateSkill: %v", err)
	}
	if info.Name != "greet" || info.Pack != "local" {
		t.Fatalf("unexpected info: %+v", info)
	}
	skillFile := filepath.Join(skill.SkillsDir(base), "local", "skills", "greet", "SKILL.md")
	data, err := os.ReadFile(skillFile)
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	if !containsAll(string(data), "name: greet", "description: Greet the user", "Say hello politely.") {
		t.Fatalf("bad SKILL.md:\n%s", data)
	}
	reg, _ := skill.LoadRegistry(base)
	e := reg.Find("local")
	if e == nil || !e.Enabled {
		t.Fatalf("local pack not registered enabled: %+v", e)
	}
}

func TestGenerateSkillRejectsBadName(t *testing.T) {
	c, _ := newTestConfigurator(t)
	for _, bad := range []string{"", "../escape", "a/b"} {
		if _, err := c.GenerateSkill(bad, "d", "i"); err == nil {
			t.Fatalf("expected error for name %q", bad)
		}
	}
}
