package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// ServerConfig describes how to start a language server.
type ServerConfig struct {
	Command string
	Args    []string
	Env     map[string]string
}

// Client is a JSON-RPC 2.0 connection to an LSP server over stdio.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	mu     sync.Mutex
	nextID int64
}

type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type rpcNotification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Start launches a language server process and performs the LSP
// initialize/initialized handshake.
func Start(ctx context.Context, cfg ServerConfig, rootDir string) (*Client, error) {
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start LSP server %q: %w", cfg.Command, err)
	}

	c := &Client{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
	}

	absRoot, _ := filepath.Abs(rootDir)
	rootURI := (&url.URL{Scheme: "file", Path: absRoot}).String()

	initParams := InitializeParams{
		RootURI:   rootURI,
		ProcessID: os.Getpid(),
		Capabilities: ClientCapabilities{
			TextDocument: TextDocumentClientCapabilities{
				Definition:     &struct{}{},
				References:     &struct{}{},
				DocumentSymbol: &struct{}{},
				Hover:          &struct{}{},
			},
		},
	}

	var initResult json.RawMessage
	if err := c.call("initialize", initParams, &initResult); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("LSP initialize: %w", err)
	}
	if err := c.notify("initialized", struct{}{}); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("LSP initialized notification: %w", err)
	}
	return c, nil
}

// Close sends shutdown/exit and kills the server process.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.notify("shutdown", nil)
		_ = c.notify("exit", nil)
		c.cmd.Process.Kill()
	}
	return nil
}

// call sends a JSON-RPC request and waits for the response.
func (c *Client) call(method string, params interface{}, result interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := atomic.AddInt64(&c.nextID, 1)
	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if err := c.write(data); err != nil {
		return err
	}

	// Read responses, skipping notifications until we get our response.
	for {
		resp, err := c.read()
		if err != nil {
			return fmt.Errorf("LSP %s: read: %w", method, err)
		}
		if resp.ID != id {
			continue // a notification or server-initiated message
		}
		if resp.Error != nil {
			return fmt.Errorf("LSP %s: %s", method, resp.Error.Message)
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	}
}

// notify sends a JSON-RPC notification (no response expected).
func (c *Client) notify(method string, params interface{}) error {
	notif := rpcNotification{JSONRPC: "2.0", Method: method, Params: params}
	data, err := json.Marshal(notif)
	if err != nil {
		return err
	}
	return c.write(data)
}

func (c *Client) write(data []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if _, err := c.stdin.Write([]byte(header)); err != nil {
		return err
	}
	_, err := c.stdin.Write(data)
	return err
}

func (c *Client) read() (*rpcResponse, error) {
	var contentLength int
	for {
		line, err := c.stdout.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Content-Length:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
			contentLength, _ = strconv.Atoi(val)
		}
	}
	if contentLength == 0 {
		return nil, fmt.Errorf("no content length in response header")
	}
	buf := make([]byte, contentLength)
	if _, err := io.ReadFull(c.stdout, buf); err != nil {
		return nil, err
	}
	var resp rpcResponse
	if err := json.Unmarshal(buf, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// fileURI converts a filesystem path to a file:// URI.
func fileURI(path string) string {
	abs, _ := filepath.Abs(path)
	u := &url.URL{Scheme: "file", Path: abs}
	return u.String()
}

// Definition requests go-to-definition at the given position.
// line is 1-indexed (converted to 0-indexed internally).
func (c *Client) Definition(ctx context.Context, file string, line, col int) ([]Location, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: fileURI(file)},
		Position:     Position{Line: line - 1, Character: col},
	}
	var raw json.RawMessage
	if err := c.call("textDocument/definition", params, &raw); err != nil {
		return nil, err
	}
	return parseLocations(raw)
}

// References requests all references to the symbol at the given position.
// line is 1-indexed.
func (c *Client) References(ctx context.Context, file string, line, col int) ([]Location, error) {
	params := ReferenceParams{
		TextDocument: TextDocumentIdentifier{URI: fileURI(file)},
		Position:     Position{Line: line - 1, Character: col},
	}
	params.Context.IncludeDeclaration = true
	var raw json.RawMessage
	if err := c.call("textDocument/references", params, &raw); err != nil {
		return nil, err
	}
	return parseLocations(raw)
}

// DocumentSymbols requests all symbols in a document.
func (c *Client) DocumentSymbols(ctx context.Context, file string) ([]Symbol, error) {
	params := DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: fileURI(file)},
	}
	var raw json.RawMessage
	if err := c.call("textDocument/documentSymbol", params, &raw); err != nil {
		return nil, err
	}
	// Result can be []Symbol or []DocumentSymbol (hierarchical). Try both.
	var symbols []Symbol
	if err := json.Unmarshal(raw, &symbols); err == nil {
		return symbols, nil
	}
	// Some servers return a flat list of DocumentSymbolInformation.
	var flat []struct {
		Name        string `json:"name"`
		Kind        int    `json:"kind"`
		Location    struct {
			URI   string `json:"uri"`
			Range Range  `json:"range"`
		} `json:"location"`
	}
	if err := json.Unmarshal(raw, &flat); err != nil {
		return nil, err
	}
	symbols = make([]Symbol, 0, len(flat))
	for _, f := range flat {
		symbols = append(symbols, Symbol{
			Name:  f.Name,
			Kind:  f.Kind,
			Range: f.Location.Range,
		})
	}
	return symbols, nil
}

// DidOpen notifies the server that a file was opened.
func (c *Client) DidOpen(ctx context.Context, file, lang, text string) error {
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":        fileURI(file),
			"languageId": lang,
			"version":    1,
			"text":       text,
		},
	}
	return c.notify("textDocument/didOpen", params)
}

// DidClose notifies the server that a file was closed.
func (c *Client) DidClose(ctx context.Context, file string) error {
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": fileURI(file),
		},
	}
	return c.notify("textDocument/didClose", params)
}

// Hover requests hover information at a position.
// line is 1-indexed.
func (c *Client) Hover(ctx context.Context, file string, line, col int) (*Hover, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: fileURI(file)},
		Position:     Position{Line: line - 1, Character: col},
	}
	var raw json.RawMessage
	if err := c.call("textDocument/hover", params, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var hover Hover
	if err := json.Unmarshal(raw, &hover); err != nil {
		return nil, err
	}
	return &hover, nil
}

// Diagnostics fetches diagnostics for a file. Not all servers support
// pull-based diagnostics (textDocument/diagnostic); for those we return
// an empty list. This is best-effort.
func (c *Client) Diagnostics(ctx context.Context, file string) ([]Diagnostic, error) {
	params := DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: fileURI(file)},
	}
	var raw json.RawMessage
	if err := c.call("textDocument/diagnostic", params, &raw); err != nil {
		// Many servers don't support pull diagnostics. Return empty
		// rather than erroring so the tool is still useful.
		return nil, nil
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var diags []Diagnostic
	if err := json.Unmarshal(raw, &diags); err != nil {
		// Might be a RelatedFullDocumentDiagnosticReport with .items
		var report struct {
			Items []Diagnostic `json:"items"`
		}
		if err2 := json.Unmarshal(raw, &report); err2 == nil {
			return report.Items, nil
		}
		return nil, err
	}
	return diags, nil
}

// Rename requests a symbol rename at a position.
// line is 1-indexed.
func (c *Client) Rename(ctx context.Context, file string, line, col int, newName string) (*WorkspaceEdit, error) {
	params := RenameParams{
		TextDocument: TextDocumentIdentifier{URI: fileURI(file)},
		Position:     Position{Line: line - 1, Character: col},
		NewName:       newName,
	}
	var raw json.RawMessage
	if err := c.call("textDocument/rename", params, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var edit WorkspaceEdit
	if err := json.Unmarshal(raw, &edit); err != nil {
		return nil, err
	}
	return &edit, nil
}

// CodeActions requests available code actions for a position.
// line is 1-indexed.
func (c *Client) CodeActions(ctx context.Context, file string, line, col int) ([]CodeAction, error) {
	// Use a zero-length range at the position — servers will return
	// actions applicable there.
	params := CodeActionParams{
		TextDocument: TextDocumentIdentifier{URI: fileURI(file)},
		Range: Range{
			Start: Position{Line: line - 1, Character: col},
			End:   Position{Line: line - 1, Character: col},
		},
	}
	var raw json.RawMessage
	if err := c.call("textDocument/codeAction", params, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var actions []CodeAction
	if err := json.Unmarshal(raw, &actions); err != nil {
		return nil, err
	}
	return actions, nil
}

func parseLocations(raw json.RawMessage) ([]Location, error) {
	var locs []Location
	if err := json.Unmarshal(raw, &locs); err == nil {
		return locs, nil
	}
	var single Location
	if err := json.Unmarshal(raw, &single); err == nil {
		return []Location{single}, nil
	}
	return nil, fmt.Errorf("could not parse locations from: %s", string(raw))
}
