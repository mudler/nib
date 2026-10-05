package types

import (
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

func TestGoalGuardDecodeErrorsAreRetained(t *testing.T) {
	for _, field := range []string{"max_reprompts: 1.5", "max_reprompts: 9999999999999999999999", "reprompt_window: [2m]"} {
		var cfg Config
		if err := yaml.Unmarshal([]byte("model: keep\ngoal:\n  "+field+"\n  check_in_delays: [3m]\n"), &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.Goal.RepromptGuardError == "" {
			t.Fatal("missing guard error")
		}
		if cfg.Model != "keep" || len(cfg.Goal.CheckInDelays) != 1 {
			t.Fatal("lost other config")
		}
		b, err := yaml.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "repromptguarderror") {
			t.Fatal("runtime error persisted")
		}
	}
}
