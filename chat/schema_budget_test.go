package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/cogito"
	"github.com/mudler/nib/types"
	openai "github.com/sashabaranov/go-openai"
)

// schemaTestTool is a ToolDefinitionInterface whose schema size the test controls
// through the description length.
type schemaTestTool struct{ desc string }

func (t schemaTestTool) Tool() openai.Tool {
	return openai.Tool{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "t", Description: t.desc}}
}

func (t schemaTestTool) Execute(map[string]any) (string, any, error) { return "", nil, nil }

func TestSchemaBudgetEstimateMatchesByteQuarter(t *testing.T) {
	tools := []cogito.ToolDefinitionInterface{schemaTestTool{desc: "one"}, schemaTestTool{desc: strings.Repeat("x", 100)}}
	sys := strings.Repeat("s", 40)

	want := len(sys)
	for _, td := range tools {
		b, err := json.Marshal(td.Tool())
		if err != nil {
			t.Fatal(err)
		}
		want += len(b)
	}
	want /= 4

	if got := estimateSchemaTokens(tools, sys); got != want {
		t.Fatalf("estimateSchemaTokens = %d, want %d", got, want)
	}
	if got := estimateSchemaTokens(nil, ""); got != 0 {
		t.Fatalf("empty estimate = %d, want 0", got)
	}
}

func TestSchemaBudgetThresholds(t *testing.T) {
	// Window 1000 → reserve min(4096, 500) = 500 → budget 500.
	// Warn when floor > 0.60*500 = 300; Error when floor > 0.90*1000 = 900.
	cases := []struct {
		name        string
		sysBytes    int
		warn, error bool
	}{
		{"small", 400, false, false},         // floor 100
		{"warn", 1400, true, false},          // floor 350
		{"error", 3800, true, true},          // floor 950
		{"at warn edge", 1200, false, false}, // floor 300, not strictly greater
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newCompactTestSession(&fakeSummaryLLM{}, 2, nil, nil)
			s.systemPrompt = strings.Repeat("p", c.sysBytes)
			got := s.SchemaBudget()
			if got.Floor != c.sysBytes/4 {
				t.Fatalf("Floor = %d, want %d", got.Floor, c.sysBytes/4)
			}
			if got.Warn != c.warn || got.Error != c.error {
				t.Fatalf("Warn/Error = %v/%v, want %v/%v", got.Warn, got.Error, c.warn, c.error)
			}
			if got.WarnThreshold != schemaWarnFraction || got.ErrorThreshold != schemaErrorFraction {
				t.Fatalf("thresholds = %v/%v", got.WarnThreshold, got.ErrorThreshold)
			}
		})
	}
}

func TestSchemaBudgetCountsRecordedTools(t *testing.T) {
	s := newCompactTestSession(&fakeSummaryLLM{}, 2, nil, nil)
	tool := schemaTestTool{desc: strings.Repeat("d", 2000)}
	opt := s.withTool(tool)
	if opt == nil {
		t.Fatal("withTool returned nil option")
	}
	want := estimateSchemaTokens([]cogito.ToolDefinitionInterface{tool}, "")
	if got := s.SchemaBudget().Floor; got != want {
		t.Fatalf("Floor = %d, want %d (recorded tool not counted)", got, want)
	}
	s.resetSchemaTools()
	if got := s.SchemaBudget().Floor; got != 0 {
		t.Fatalf("Floor after reset = %d, want 0", got)
	}
}

// promptUsageLLM reports a fixed prompt-token count for every request.
type promptUsageLLM struct {
	cogito.LLM
	prompt int
}

func (u *promptUsageLLM) CreateChatCompletion(_ context.Context, _ openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	return cogito.LLMReply{}, cogito.LLMUsage{PromptTokens: u.prompt}, nil
}

// mcpLikeTools builds n tools the session never registered, each with a
// description of size bytes.
func mcpLikeTools(n, size int) []openai.Tool {
	out := make([]openai.Tool, n)
	for i := range out {
		out[i] = openai.Tool{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{
			Name: fmt.Sprintf("mcp_tool_%d", i), Description: strings.Repeat("m", size)}}
	}
	return out
}

func TestSchemaBudgetMeasuredFromRequest(t *testing.T) {
	s := newCompactTestSession(&fakeSummaryLLM{}, 2, nil, nil)
	s.systemPrompt = "short"
	before := s.SchemaBudget()
	if before.Measured {
		t.Fatal("Measured before any request")
	}

	tools := mcpLikeTools(20, 400)
	sys := strings.Repeat("s", 800)
	req := openai.ChatCompletionRequest{
		Messages: []openai.ChatCompletionMessage{{Role: "system", Content: sys}, {Role: "user", Content: "hi"}},
		Tools:    tools,
	}
	llm := trackUsage(&promptUsageLLM{}, &s.live, s.requestLimits)
	if _, _, err := llm.CreateChatCompletion(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(tools)
	want := (len(b) + len(sys)) / 4

	got := s.SchemaBudget()
	if !got.Measured {
		t.Fatal("Measured = false after a request")
	}
	if got.Floor != want {
		t.Fatalf("Floor = %d, want %d (tools the request carried)", got.Floor, want)
	}
	if !got.Error {
		t.Fatalf("floor %d over a 1000-token window must set Error", got.Floor)
	}
}

func TestSchemaBudgetUsageCalibration(t *testing.T) {
	s := newCompactTestSession(&fakeSummaryLLM{}, 2, nil, nil)
	tools := mcpLikeTools(1, 40)
	user := strings.Repeat("u", 400) // 100 tokens
	req := openai.ChatCompletionRequest{
		Messages: []openai.ChatCompletionMessage{{Role: "system", Content: "sys"}, {Role: "user", Content: user}},
		Tools:    tools,
	}
	llm := trackUsage(&promptUsageLLM{prompt: 700}, &s.live, s.requestLimits)
	if _, _, err := llm.CreateChatCompletion(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got := s.SchemaBudget().Floor; got != 700-100 {
		t.Fatalf("Floor = %d, want the backend overhead %d", got, 600)
	}

	// A report below the measurement keeps the measurement.
	llm = trackUsage(&promptUsageLLM{prompt: 101}, &s.live, s.requestLimits)
	if _, _, err := llm.CreateChatCompletion(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(tools)
	if got, want := s.SchemaBudget().Floor, (len(b)+len("sys"))/4; got != want {
		t.Fatalf("Floor = %d, want the measurement %d", got, want)
	}
}

// connectTestMCP stands up an in-process MCP server with n tools whose
// descriptions are size bytes, and returns a connected client session.
func connectTestMCP(t *testing.T, name string, n, size int) *sdkmcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: name, Version: "v0"}, nil)
	for i := 0; i < n; i++ {
		sdkmcp.AddTool(server, &sdkmcp.Tool{Name: fmt.Sprintf("%s_%d", name, i), Description: strings.Repeat("d", size)},
			func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, any, error) {
				return &sdkmcp.CallToolResult{}, nil, nil
			})
	}
	serverT, clientT := sdkmcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "v0"}, nil)
	sess, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func TestMCPSchemaCostAttributesAndSorts(t *testing.T) {
	s := newReloadTestSession(t)
	s.cfgClients["small"] = connectTestMCP(t, "small", 2, 40)
	s.cfgClients["big"] = connectTestMCP(t, "big", 5, 2000)
	s.cfgClients["mid"] = connectTestMCP(t, "mid", 3, 400)

	costs := s.mcpSchemaCosts(context.Background())
	if len(costs) != 3 {
		t.Fatalf("costs = %+v, want 3 servers", costs)
	}
	names := []string{costs[0].Server, costs[1].Server, costs[2].Server}
	if names[0] != "big" || names[1] != "mid" || names[2] != "small" {
		t.Fatalf("order = %v, want big, mid, small", names)
	}
	if costs[0].Tools != 5 || costs[1].Tools != 3 || costs[2].Tools != 2 {
		t.Fatalf("tool counts = %+v", costs)
	}
	if costs[0].Tokens < 5*2000/4 {
		t.Fatalf("big tokens = %d, want at least %d", costs[0].Tokens, 5*2000/4)
	}

	// Cached until the set of servers changes.
	s.cfgClients["late"] = connectTestMCP(t, "late", 1, 10)
	if got := s.mcpSchemaCosts(context.Background()); len(got) != 3 {
		t.Fatalf("cache not used: %d servers", len(got))
	}
	s.cfgServers = map[string]types.MCPServer{}
	if err := s.ReconcileMCPServers(map[string]types.MCPServer{}); err != nil {
		t.Fatal(err)
	}
	if got := s.mcpSchemaCosts(context.Background()); len(got) != 0 {
		t.Fatalf("after reconcile removed every server: %+v", got)
	}
}

func TestSchemaBudgetEstimateIncludesMCPTools(t *testing.T) {
	s := newReloadTestSession(t)
	s.compaction = types.CompactionConfig{MaxContextTokens: 100000}
	s.cfgClients["big"] = connectTestMCP(t, "big", 4, 1000)
	sb := s.SchemaBudget()
	if sb.Measured {
		t.Fatal("Measured before any request")
	}
	if len(sb.Servers) != 1 || sb.Floor < sb.Servers[0].Tokens || sb.Servers[0].Tokens < 1000 {
		t.Fatalf("estimate blind to MCP tools: %+v", sb)
	}
}

func TestSchemaBudgetNoticeOnce(t *testing.T) {
	s := newReloadTestSession(t)
	s.compaction = types.CompactionConfig{MaxContextTokens: 100000}
	for i, n := range []int{8, 6, 4, 2} {
		name := fmt.Sprintf("srv%d", i)
		s.cfgClients[name] = connectTestMCP(t, name, n, 4000)
	}
	var notices []string
	s.callbacks.OnStatus = func(m string) { notices = append(notices, m) }

	// Window 100000, budget 100000-min(4096, 50000) = 95904:
	// warn above 57542, error above 90000.
	s.live.recordFloor(1000)
	s.notifySchemaBudget()
	if len(notices) != 0 {
		t.Fatalf("notice below the threshold: %q", notices)
	}
	s.live.recordFloor(60000)
	s.notifySchemaBudget()
	s.notifySchemaBudget()
	if len(notices) != 1 {
		t.Fatalf("warn notices = %d, want 1: %q", len(notices), notices)
	}
	n := notices[0]
	for _, want := range []string{"60k", "100k", "srv0", "srv1", "srv2", "8 tools", "…", "disable MCP servers"} {
		if !strings.Contains(n, want) {
			t.Fatalf("notice %q lacks %q", n, want)
		}
	}
	if strings.Contains(n, "srv3") {
		t.Fatalf("notice names more than three servers: %q", n)
	}
	if strings.Index(n, "srv0") > strings.Index(n, "srv1") {
		t.Fatalf("largest server not first: %q", n)
	}

	s.live.recordFloor(95000)
	s.notifySchemaBudget()
	s.notifySchemaBudget()
	if len(notices) != 2 {
		t.Fatalf("notices after error threshold = %d, want 2", len(notices))
	}
}
