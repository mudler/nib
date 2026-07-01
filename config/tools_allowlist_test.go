package config

import (
	"testing"

	"github.com/mudler/nib/types"
	"gopkg.in/yaml.v3"
)

// The tool allowlist must load from the config file — Load() unmarshals the same
// way (loadFromFile -> yaml.Unmarshal into types.Config), so this proves the
// `builtin_tools:` key is wired through to Config.BuiltinTools.
func TestToolsAllowlistUnmarshalsFromConfig(t *testing.T) {
	var c types.Config
	if err := yaml.Unmarshal([]byte("builtin_tools:\n  - read\n  - bash\n  - web_search\n"), &c); err != nil {
		t.Fatal(err)
	}
	if len(c.BuiltinTools) != 3 || c.BuiltinTools[0] != "read" || c.BuiltinTools[2] != "web_search" {
		t.Fatalf("config `builtin_tools:` did not load into Config.BuiltinTools: %+v", c.BuiltinTools)
	}
}
