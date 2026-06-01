package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/wiz/types"
)

// loadSkillInput is the argument to the load_skill tool.
type loadSkillInput struct {
	Name string `json:"name" jsonschema:"the name of the skill to load"`
}

// loadSkillOutput is the result of the load_skill tool.
type loadSkillOutput struct {
	Name         string `json:"name" jsonschema:"the requested skill name"`
	Instructions string `json:"instructions" jsonschema:"the skill's full instructions"`
	Found        bool   `json:"found" jsonschema:"whether the skill was found"`
	Error        string `json:"error,omitempty" jsonschema:"error message if not found"`
}

// skillIndex builds a name→instructions map and the ordered list of names.
func skillIndex(skills []types.Skill) (map[string]string, []string) {
	index := make(map[string]string, len(skills))
	names := make([]string, 0, len(skills))
	for _, s := range skills {
		index[s.Name] = s.Instructions
		names = append(names, s.Name)
	}
	return index, names
}

// loadSkillResult looks a skill up by name in the index.
func loadSkillResult(index map[string]string, in loadSkillInput) loadSkillOutput {
	body, ok := index[in.Name]
	if !ok {
		return loadSkillOutput{Name: in.Name, Found: false, Error: fmt.Sprintf("unknown skill %q", in.Name)}
	}
	return loadSkillOutput{Name: in.Name, Instructions: body, Found: true}
}

// StartSkillsMCPServer runs an in-memory MCP server exposing a load_skill tool
// that returns a skill's full instructions by name.
func StartSkillsMCPServer(ctx context.Context, transport mcp.Transport, skills []types.Skill) error {
	index, names := skillIndex(skills)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "skills",
		Version: "v1.0.0",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "load_skill",
		Description: fmt.Sprintf("Load the full instructions for a skill by name, then follow them. Available skills: %v.", names),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in loadSkillInput) (*mcp.CallToolResult, loadSkillOutput, error) {
		return nil, loadSkillResult(index, in), nil
	})

	return server.Run(ctx, transport)
}
