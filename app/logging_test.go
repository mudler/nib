package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

func TestStartupLogsUseModeDestinationBeforeMCPInitialization(t *testing.T) {
	for _, mode := range []string{"--tui", "--cli", "mcp"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			var stderr bytes.Buffer
			code := runCtx(context.Background(), Options{
				Args: []string{mode}, BaseDir: root, SkipBareEnv: true, SkipSetup: true,
				Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: &stderr,
				Overrides: types.Config{Provider: "nonexistent-test-provider", Model: "test-model", APIKey: "must-not-appear-in-logs", LogLevel: "debug"},
			})
			if code != 1 {
				t.Fatalf("got exit %d, want initialization failure", code)
			}
			path := filepath.Join(root, "nib.log")
			var logs string
			if mode == "--tui" {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				logs = string(data)
				if strings.Contains(stderr.String(), "Starting nib") {
					t.Fatal("TUI debug logs reached terminal")
				}
			} else {
				logs = stderr.String()
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("stdio mode created TUI log: %v", err)
				}
			}
			for _, message := range []string{"Starting nib", "Initializing built-in MCP servers", "Built-in MCP initialization failed"} {
				if !strings.Contains(logs, message) {
					t.Errorf("missing startup log %q: %s", message, logs)
				}
			}
			if strings.Contains(logs, "must-not-appear-in-logs") {
				t.Fatal("credentials were logged")
			}
		})
	}
}
