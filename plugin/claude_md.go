package plugin

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/nib/types"

	"gopkg.in/yaml.v3"
)

// splitFrontmatter separates a markdown file's leading `--- ... ---` YAML
// frontmatter from its body. If there is no frontmatter, fm is empty and body
// is the whole input.
func splitFrontmatter(data []byte) (fm []byte, body string) {
	s := bufio.NewScanner(bytes.NewReader(data))
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !s.Scan() {
		return nil, string(data)
	}
	if strings.TrimRight(s.Text(), "\r") != "---" {
		return nil, string(data)
	}
	var fmBuf, bodyBuf bytes.Buffer
	inBody := false
	for s.Scan() {
		line := s.Text()
		if !inBody && strings.TrimRight(line, "\r") == "---" {
			inBody = true
			continue
		}
		if inBody {
			bodyBuf.WriteString(line)
			bodyBuf.WriteString("\n")
		} else {
			fmBuf.WriteString(line)
			fmBuf.WriteString("\n")
		}
	}
	if !inBody {
		return nil, string(data)
	}
	return fmBuf.Bytes(), bodyBuf.String()
}

// claudeToolsField parses a Claude `tools`/`allowed-tools` frontmatter value,
// which may be a YAML list or a comma-separated string, into wiz tool names.
type claudeToolsField struct{ tools []string }

func (f *claudeToolsField) UnmarshalYAML(value *yaml.Node) error {
	var list []string
	if err := value.Decode(&list); err == nil {
		f.tools = aliasClaudeTools(list)
		return nil
	}
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parts := strings.Split(s, ",")
	raw := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			raw = append(raw, p)
		}
	}
	f.tools = aliasClaudeTools(raw)
	return nil
}

// ParseSkillMarkdown parses a SKILL.md byte slice into its frontmatter fields
// (name, description, wiz tool names) and body. Tool names are aliased from
// Claude names to wiz names. Shared by the plugin Claude adapter and the
// standalone skill installer (skill package).
func ParseSkillMarkdown(data []byte) (name, description string, tools []string, body string) {
	fm, body := splitFrontmatter(data)
	var meta struct {
		Name        string           `yaml:"name"`
		Description string           `yaml:"description"`
		Tools       claudeToolsField `yaml:"allowed-tools"`
	}
	_ = yaml.Unmarshal(fm, &meta)
	return meta.Name, meta.Description, meta.Tools.tools, body
}

// loadClaudeSkills reads skills/<name>/SKILL.md files into SkillSpecs (bodies inline).
func loadClaudeSkills(root string) []SkillSpec {
	entries, err := os.ReadDir(filepath.Join(root, "skills"))
	if err != nil {
		return nil
	}
	var out []SkillSpec
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, "skills", e.Name(), "SKILL.md"))
		if err != nil {
			continue
		}
		name, desc, tools, body := ParseSkillMarkdown(data)
		if name == "" {
			name = e.Name()
		}
		out = append(out, SkillSpec{
			Name:         name,
			Description:  desc,
			Instructions: InstructionsSpec{Inline: body},
			Tools:        tools,
		})
	}
	return out
}

// loadClaudeCommands reads commands/*.md files into CommandConfigs.
func loadClaudeCommands(root string) []types.CommandConfig {
	entries, err := os.ReadDir(filepath.Join(root, "commands"))
	if err != nil {
		return nil
	}
	var out []types.CommandConfig
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, "commands", e.Name()))
		if err != nil {
			continue
		}
		fm, body := splitFrontmatter(data)
		var meta struct {
			Description string `yaml:"description"`
		}
		_ = yaml.Unmarshal(fm, &meta)
		out = append(out, types.CommandConfig{
			Name:        strings.TrimSuffix(e.Name(), ".md"),
			Description: meta.Description,
			Prompt:      strings.ReplaceAll(body, "$ARGUMENTS", "{{.Args}}"),
		})
	}
	return out
}

// loadClaudeAgents reads agents/*.md files into AgentTypeConfigs.
func loadClaudeAgents(root string) []types.AgentTypeConfig {
	entries, err := os.ReadDir(filepath.Join(root, "agents"))
	if err != nil {
		return nil
	}
	var out []types.AgentTypeConfig
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, "agents", e.Name()))
		if err != nil {
			continue
		}
		fm, body := splitFrontmatter(data)
		var meta struct {
			Name        string           `yaml:"name"`
			Description string           `yaml:"description"`
			Tools       claudeToolsField `yaml:"tools"`
			Model       string           `yaml:"model"`
		}
		_ = yaml.Unmarshal(fm, &meta)
		name := meta.Name
		if name == "" {
			name = strings.TrimSuffix(e.Name(), ".md")
		}
		out = append(out, types.AgentTypeConfig{
			Name:         name,
			Description:  meta.Description,
			SystemPrompt: body,
			Tools:        meta.Tools.tools,
			Model:        meta.Model,
		})
	}
	return out
}
