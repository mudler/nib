package config

import (
	"testing"

	"github.com/mudler/nib/types"
	"gopkg.in/yaml.v3"
)

// The tool allowlist must load from the config file — Load() unmarshals the same
// way (loadFromFile -> yaml.Unmarshal into types.Config), so this proves the
// `tools:` key is wired through to Config.Tools.
func TestToolsAllowlistUnmarshalsFromConfig(t *testing.T) {
	var c types.Config
	if err := yaml.Unmarshal([]byte("tools:\n  - read\n  - bash\n  - web_search\n"), &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Tools) != 3 || c.Tools[0] != "read" || c.Tools[2] != "web_search" {
		t.Fatalf("config `tools:` did not load into Config.Tools: %+v", c.Tools)
	}
}
