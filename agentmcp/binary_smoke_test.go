package agentmcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestBinaryE2EStdio drives the REAL compiled `nib mcp` binary as a subprocess
// over a real stdio pipe with a real MCP client, against a fake LLM. This covers
// what the in-process e2e tests cannot: that the launched binary boots in mcp
// mode, keeps stdout clean for JSON-RPC (no log/print contamination breaks the
// transport), and answers a converse end-to-end. It builds nib itself, so it
// needs no setup; it skips under `go test -short`.
func TestBinaryE2EStdio(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary e2e under -short (it compiles nib)")
	}

	bin := filepath.Join(t.TempDir(), "nib")
	build := exec.Command("go", "build", "-o", bin, "github.com/mudler/nib")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build nib: %v\n%s", err, out)
	}

	llm := fakeLLM(t, "Two plus two is four.")
	defer llm.Close()

	cmd := exec.Command(bin, "mcp")
	cmd.Env = append(os.Environ(),
		"MODEL=fake",
		"API_KEY=sk-test",
		"BASE_URL="+llm.URL+"/v1",
		"LOG_LEVEL=error",
	)
	cmd.Stderr = os.Stderr // surface server logs, keep stdout clean for JSON-RPC

	client := mcp.NewClient(&mcp.Implementation{Name: "smoke", Version: "v0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect to launched `nib mcp`: %v", err)
	}
	defer cs.Close()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "converse",
		Arguments: map[string]any{"utterance": "what is two plus two?"},
	})
	if err != nil {
		t.Fatalf("converse over stdio: %v", err)
	}
	var out converseOut
	decodeStructured(t, res, &out)
	if out.Reply == "" {
		t.Fatalf("expected a non-empty reply from the launched binary, got %+v", out)
	}
	t.Logf("binary e2e reply: %q (pending=%v turn=%d)", out.Reply, out.Pending, out.Turn)
}
