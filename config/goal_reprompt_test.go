package config

import (
	"github.com/mudler/nib/types"

	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGoalRepromptSettings(t *testing.T) {
	for _, tc := range []struct{ key, raw, want string }{
		{"goal.max_reprompts", "-1", "-1"}, {"goal.max_reprompts", "0", "10"},
		{"goal.reprompt_window", "0", "2m0s"}, {"goal.reprompt_window", "500ms", "500ms"},
	} {
		t.Run(tc.key+tc.raw, func(t *testing.T) {
			s, err := LookupSetting(tc.key)
			if err != nil {
				t.Fatal(err)
			}
			v, err := s.Parse(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			var cfg types.Config
			s.Apply(&cfg, v)
			if got := s.Format(cfg); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := WriteSetting(path, tc.key, v); err != nil {
				t.Fatal(err)
			}
			loaded, _, err := FileSettings(path)
			if err != nil {
				t.Fatal(err)
			}
			if s.Format(loaded) != tc.want {
				t.Fatal("round trip changed value")
			}
		})
	}
	for key, values := range map[string][]string{
		"goal.max_reprompts":        {"-2", "1.5", "999999999999999999999999"},
		"goal.reprompt_window":      {"-1s", "wrong", "1", "999999999999999h"},
		"agent_options.max_retries": {"-1"},
	} {
		for _, v := range values {
			s, err := LookupSetting(key)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Parse(v); err == nil {
				t.Fatalf("accepted %s=%s", key, v)
			}
		}
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("model: keep\n")
	os.WriteFile(path, original, 0600)
	for key, v := range map[string]any{"goal.max_reprompts": -2, "goal.reprompt_window": "bad"} {
		if err := WriteSetting(path, key, v); err == nil {
			t.Fatalf("wrote invalid %s", key)
		}
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(original) {
		t.Fatal("invalid write changed file")
	}
}

func TestGoalRepromptDecodeRetainsFile(t *testing.T) {
	for _, value := range []string{"1.5", "999999999999999999999999", "[1]"} {
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, "config.yaml"), []byte("model: keep\ngoal:\n  max_reprompts: "+value+"\n"), 0600)
		cfg := loadFromFileIn(root)
		if cfg.Model != "keep" {
			t.Fatal("lost containing file")
		}
		// Invalid guard fields must survive as a startup validation failure.
		if _, _, err := ParseGoalRepromptGuard(cfg.Goal); err == nil {
			t.Fatal("lost guard validation error")
		}
	}
}

func TestParseGoalRepromptGuard(t *testing.T) {
	for _, tc := range []struct {
		g      types.GoalConfig
		max    int
		window time.Duration
		bad    bool
	}{
		{types.GoalConfig{}, 10, 2 * time.Minute, false},
		{types.GoalConfig{RepromptWindow: "0"}, 10, 2 * time.Minute, false},
		{types.GoalConfig{RepromptWindow: "0s"}, 10, 2 * time.Minute, false},
		{types.GoalConfig{MaxReprompts: -1, RepromptWindow: "500ms"}, -1, 500 * time.Millisecond, false},
		{types.GoalConfig{MaxReprompts: 1, RepromptWindow: "3m"}, 1, 3 * time.Minute, false},
		{types.GoalConfig{MaxReprompts: -2}, 0, 0, true},
		{types.GoalConfig{MaxReprompts: -1, RepromptWindow: "bad"}, 0, 0, true},
		{types.GoalConfig{RepromptWindow: "-1s"}, 0, 0, true},
		{types.GoalConfig{RepromptWindow: "999999999999999h"}, 0, 0, true},
	} {
		n, w, err := ParseGoalRepromptGuard(tc.g)
		if (err != nil) != tc.bad {
			t.Fatalf("%+v: %v", tc.g, err)
		}
		if !tc.bad && (n != tc.max || w != tc.window) {
			t.Fatalf("got %d %s", n, w)
		}
	}
}

func TestGoalRepromptLoadingPrecedence(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("model: keep\ngoal:\n  max_reprompts: 3\n  reprompt_window: 30s\n"), 0600); err != nil {
		t.Fatal(err)
	}
	seeds := types.Config{Goal: types.GoalConfig{MaxReprompts: 7, RepromptWindow: "7m"}}
	cfg := LoadWith(LoadOptions{BaseDir: root, SkipBareEnv: true, Defaults: seeds})
	n, w, err := ParseGoalRepromptGuard(cfg.Goal)
	if err != nil || n != 3 || w != 30*time.Second {
		t.Fatalf("file precedence: %d %s %v", n, w, err)
	}
	cfg = LoadWith(LoadOptions{BaseDir: root, SkipBareEnv: true, Defaults: seeds, Overrides: types.Config{Goal: types.GoalConfig{MaxReprompts: -1, RepromptWindow: "1m"}}})
	n, w, err = ParseGoalRepromptGuard(cfg.Goal)
	if err != nil || n != -1 || w != time.Minute {
		t.Fatalf("override precedence: %d %s %v", n, w, err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("model: keep\ngoal:\n  max_reprompts: 1.5\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg = LoadWith(LoadOptions{BaseDir: root, SkipBareEnv: true, Overrides: seeds})
	if _, _, err := ParseGoalRepromptGuard(cfg.Goal); err == nil {
		t.Fatal("override hid guard decode error")
	}
	if cfg.Model != "keep" {
		t.Fatal("lost containing file")
	}
}
