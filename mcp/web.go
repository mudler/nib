package mcp

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/llmprovider"
	"github.com/mudler/nib/plugin"
	"github.com/mudler/nib/provenance"
	"github.com/mudler/nib/types"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// webModelTransport binds the web tools to the owning session's current
// provider without sharing its conversation or mutable LLM client. Like the
// underlying in-memory transport, it belongs to one client connection.
type webModelTransport struct {
	mcp.Transport
	mu      sync.RWMutex
	current func() types.ModelProviderConfig
}

func (t *webModelTransport) BindWebModel(current func() types.ModelProviderConfig) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.current = current
}

func (t *webModelTransport) model() types.ModelProviderConfig {
	t.mu.RLock()
	current := t.current
	t.mu.RUnlock()
	return current()
}

// StartWebMCPServer starts the web MCP server exposing web_fetch and web_search.
// web_fetch reuses the session's main model and request options (model, API key,
// base URL, metadata, and reasoning effort) for its extraction pass.
//
// The extraction LLM is built with the credential store so that providers whose
// API key was stored by `nib login` (rather than set in config or env) are
// authenticated. Without the store, web_fetch would send requests with no API
// key and receive 401 from providers like regolo.
func StartWebMCPServer(ctx context.Context, transport mcp.Transport, cfg types.Config) error {
	return startWebMCPServer(ctx, transport, cfg, cfg.ResolvedMainModel)
}

func startWebMCPServer(ctx context.Context, transport mcp.Transport, cfg types.Config, current func() types.ModelProviderConfig) error {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "web",
		Version: "v1.0.0",
	}, nil)

	store := auth.NewStore(filepath.Join(plugin.BaseDirIn(cfg.BaseDir), "credentials.json"))
	classifier, err := provenance.ClassifierForConfig(cfg)
	if err != nil {
		return err
	}
	ws := &webServer{
		newLLM:     func() (cogito.LLM, error) { return llmprovider.NewWithStore(current(), store) },
		classifier: classifier,
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "web_fetch",
		Description: "Fetch a URL and answer a prompt against its content. Both url and prompt are required. Returns the model's answer based on the page text. Works with any host including localhost.",
	}, ws.fetch)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "web_search",
		Description: "Search the web via DuckDuckGo. Returns up to max_results (default 5) structured results with title, url, and snippet.",
	}, ws.search)

	return server.Run(ctx, transport)
}
