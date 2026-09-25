package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
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

// skewLLM reports the whole request (messages and tool schemas) at byte/4
// times ratio, standing in for a backend whose tokenizer counts more than the
// byte/4 estimate.
type skewLLM struct {
	cogito.LLM
	ratio float64
}

func (u *skewLLM) CreateChatCompletion(_ context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	n := estimateTokens(req.Messages) + toolSchemaBytes(req.Tools)/4
	return cogito.LLMReply{}, cogito.LLMUsage{PromptTokens: int(float64(n) * u.ratio)}, nil
}

// skewRequest is a request with about schemaTokens of tools and system
// prompt and historyTokens of byte/4 conversation. It returns the byte/4 size
// of the tools and system messages too.
func skewRequest(schemaTokens, historyTokens int) (openai.ChatCompletionRequest, int) {
	tools := mcpLikeTools(schemaTokens/1000, 3900)
	sys := "you are nib"
	req := openai.ChatCompletionRequest{
		Messages: []openai.ChatCompletionMessage{
			{Role: "system", Content: sys},
			{Role: "user", Content: strings.Repeat("u", historyTokens*4)},
		},
		Tools: tools,
	}
	return req, (toolSchemaBytes(tools) + len(sys)) / 4
}

func near(got, want int) bool {
	d := got - want
	return d >= -want/100-2 && d <= want/100+2
}

// The worked example: 5k of schemas, 60k of history, a backend counting 1.5x.
// The floor is the schemas at 1.5x, not the whole residual, and it does not
// grow with the history.
func TestSchemaBudgetScalesByTokenizerRatio(t *testing.T) {
	s := newCompactTestSession(&fakeSummaryLLM{}, 2, nil, nil)
	llm := trackUsage(&skewLLM{ratio: 1.5}, &s.live, s.requestLimits)

	req, raw := skewRequest(5000, 60000)
	if _, _, err := llm.CreateChatCompletion(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	want := raw * 3 / 2
	if got := s.SchemaBudget().Floor; !near(got, want) {
		t.Fatalf("Floor = %d, want about %d (schemas x 1.5), not the residual", got, want)
	}

	req, _ = skewRequest(5000, 120000)
	if _, _, err := llm.CreateChatCompletion(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got := s.SchemaBudget().Floor; !near(got, want) {
		t.Fatalf("Floor = %d after the history doubled, want about %d", got, want)
	}
}

// The ratio is clamped to [1, maxTokenizerRatio]: a report below the estimate
// keeps the measurement, and a wild one cannot multiply the floor without end.
func TestSchemaBudgetRatioClamped(t *testing.T) {
	s := newCompactTestSession(&fakeSummaryLLM{}, 2, nil, nil)
	req, raw := skewRequest(2000, 1000)

	llm := trackUsage(&skewLLM{ratio: 0.5}, &s.live, s.requestLimits)
	if _, _, err := llm.CreateChatCompletion(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got := s.SchemaBudget().Floor; got != raw {
		t.Fatalf("Floor = %d with a report below the estimate, want the measurement %d", got, raw)
	}

	llm = trackUsage(&skewLLM{ratio: 10}, &s.live, s.requestLimits)
	if _, _, err := llm.CreateChatCompletion(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got, want := s.SchemaBudget().Floor, int(float64(raw)*maxTokenizerRatio); got != want {
		t.Fatalf("Floor = %d with a 10x report, want %d (clamped to %vx)", got, want, maxTokenizerRatio)
	}
}

// Image parts are not in the byte/4 estimate, so a request that carries one
// says nothing about the tokenizer: it does not change the ratio.
func TestSchemaBudgetImageKeepsRatio(t *testing.T) {
	s := newCompactTestSession(&fakeSummaryLLM{}, 2, nil, nil)
	llm := trackUsage(&skewLLM{ratio: 1.5}, &s.live, s.requestLimits)
	req, raw := skewRequest(2000, 1000)
	if _, _, err := llm.CreateChatCompletion(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	want := s.SchemaBudget().Floor
	if !near(want, raw*3/2) {
		t.Fatalf("Floor = %d, want about %d", want, raw*3/2)
	}

	img := req
	img.Messages = append(slices.Clone(req.Messages), openai.ChatCompletionMessage{Role: "user", MultiContent: []openai.ChatMessagePart{
		{Type: openai.ChatMessagePartTypeText, Text: "look"},
		{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: "data:image/png;base64,AAAA"}},
	}})
	llm = trackUsage(&skewLLM{ratio: 4}, &s.live, s.requestLimits)
	if _, _, err := llm.CreateChatCompletion(context.Background(), img); err != nil {
		t.Fatal(err)
	}
	if got := s.SchemaBudget().Floor; got != want {
		t.Fatalf("Floor = %d after a request with an image, want the previous %d", got, want)
	}

	// With no earlier calibration, an image request leaves the ratio at 1.
	s2 := newCompactTestSession(&fakeSummaryLLM{}, 2, nil, nil)
	llm = trackUsage(&skewLLM{ratio: 4}, &s2.live, s2.requestLimits)
	if _, _, err := llm.CreateChatCompletion(context.Background(), img); err != nil {
		t.Fatal(err)
	}
	if got := s2.SchemaBudget().Floor; got != raw {
		t.Fatalf("Floor = %d, want the unscaled measurement %d", got, raw)
	}
}

// The schema-budget notice reports the corrected size, not the tokenizer skew
// on the whole conversation.
func TestSchemaBudgetNoticeCorrectedSize(t *testing.T) {
	s := newCompactTestSession(&fakeSummaryLLM{}, 2, nil, nil)
	s.compaction.MaxContextTokens = 20000 // budget 15904, warn above 9542
	var notices []string
	s.callbacks.OnStatus = func(m string) { notices = append(notices, m) }

	llm := trackUsage(&skewLLM{ratio: 1.5}, &s.live, s.requestLimits)
	req, raw := skewRequest(8000, 60000)
	if _, _, err := llm.CreateChatCompletion(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	s.notifySchemaBudget()
	if len(notices) != 1 {
		t.Fatalf("notices = %q, want one", notices)
	}
	if want := fmtTokensK(raw * 3 / 2); !strings.Contains(notices[0], "use "+want+" ") {
		t.Fatalf("notice %q does not report the corrected %s", notices[0], want)
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
