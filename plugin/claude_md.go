package plugin

import (
	"github.com/mudler/nib/hooks"
	"github.com/mudler/nib/plugin/claudeplugin"

	"gopkg.in/yaml.v3"
)

// claudeToolsField parses a Claude `tools`/`allowed-tools` frontmatter value,
// which may be a YAML list or a comma-separated string, into nib tool names.
type claudeToolsField struct{ tools []string }

func (f *claudeToolsField) UnmarshalYAML(value *yaml.Node) error {
	var list []string
	if err := value.Decode(&list); err != nil {
		var s string
		if err := value.Decode(&s); err != nil {
			return err
		}
		list = claudeplugin.SplitToolList(s)
	}
	f.tools, _ = hooks.ClaudeTools.Map(list)
	return nil
}

// ParseSkillMarkdown parses a SKILL.md byte slice into its frontmatter fields
// (name, description, nib tool names) and body. Tool names are mapped from
// Claude Code names to nib names. Shared by the standalone skill installer
// (skill package) and the catalog crawler.
func ParseSkillMarkdown(data []byte) (name, description string, tools []string, body string) {
	fm, body := claudeplugin.SplitFrontmatter(data)
	var meta struct {
		Name        string           `yaml:"name"`
		Description string           `yaml:"description"`
		Tools       claudeToolsField `yaml:"allowed-tools"`
	}
	_ = yaml.Unmarshal(fm, &meta)
	return meta.Name, meta.Description, meta.Tools.tools, body
}
