package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mudler/cogito"
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
