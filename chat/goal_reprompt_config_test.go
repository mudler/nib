package chat

import (
	"context"
	configpkg "github.com/mudler/nib/config"
	"github.com/mudler/nib/types"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewSessionRejectsInvalidGoalGuard(t *testing.T) {
	for _, tc := range []struct{ body, key string }{
		{"max_reprompts: -2", "max_reprompts"}, {"max_reprompts: 1.5", "max_reprompts"},
		{"max_reprompts: 999999999999999999999999", "max_reprompts"},
		{"max_reprompts: -1\n  reprompt_window: bad", "reprompt_window"},
		{"reprompt_window: -1s", "reprompt_window"}, {"reprompt_window: 123", "reprompt_window"},
		{"reprompt_window: [1]", "reprompt_window"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			data := []byte("goal:\n  " + tc.body + "\n")
			var direct types.Config
			if err := yaml.Unmarshal(data, &direct); err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			os.WriteFile(filepath.Join(root, "config.yaml"), data, 0600)
			loaded := configpkg.LoadWith(configpkg.LoadOptions{BaseDir: root, SkipBareEnv: true})
			for _, cfg := range []types.Config{direct, loaded} {
				s, err := NewSession(context.Background(), cfg, Callbacks{})
				if s != nil {
					s.Close()
				}
				if err == nil || !strings.Contains(err.Error(), "goal."+tc.key) {
					t.Fatalf("want guard validation error, got %v", err)
				}
			}
		})
	}
}
