package chat

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
	openai "github.com/sashabaranov/go-openai"
)

// overflowFixture is one file of chat/testdata/overflow: an error string as
// nib's error chain prints it, and what classifyOverflow must make of it.
type overflowFixture struct {
	Provider string `json:"provider"`
	Source   string `json:"source"`
	Raw      string `json:"raw"`
	Kind     string `json:"kind"`
	Window   int    `json:"window"`
	Total    int    `json:"total"`
	Input    int    `json:"input"`
	Output   int    `json:"output"`
	// Status, when set, is the HTTP status classifyOverflow must report.
	Status int `json:"status"`
}

func loadOverflowFixtures(t *testing.T) map[string]overflowFixture {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "overflow", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no fixtures in testdata/overflow")
	}
	out := make(map[string]overflowFixture, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var f overflowFixture
		if err := json.Unmarshal(b, &f); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		out[strings.TrimSuffix(filepath.Base(p), ".json")] = f
	}
	return out
}

// Every fixture classifies to its kind and its figures. A new provider is one
// new file in the folder.
func TestOverflowPatternFixtures(t *testing.T) {
	for name, f := range loadOverflowFixtures(t) {
		t.Run(name, func(t *testing.T) {
			got := classifyOverflow(errors.New(f.Raw))
			if got.Kind.String() != f.Kind {
				t.Fatalf("kind = %s (pattern %q), want %s", got.Kind, got.Pattern, f.Kind)
			}
			if got.Window != f.Window || got.Total != f.Total || got.Input != f.Input || got.Output != f.Output {
				t.Fatalf("figures = window %d total %d input %d output %d, want %d %d %d %d",
					got.Window, got.Total, got.Input, got.Output, f.Window, f.Total, f.Input, f.Output)
			}
			if f.Status != 0 && got.Status != f.Status {
				t.Fatalf("status = %d, want %d", got.Status, f.Status)
			}
			if got.Kind != KindNone && got.Pattern == "" {
				t.Fatal("a classified error must name the pattern that matched")
			}
		})
	}
}

// The two real regolo captures, verbatim, on both of cogito's paths.
func TestOverflowPatternRegoloCaptures(t *testing.T) {
	fx := loadOverflowFixtures(t)
	for _, name := range []string{"regolo-vllm-budget-stream", "regolo-vllm-budget-nonstream"} {
		f, ok := fx[name]
		if !ok {
			t.Fatalf("fixture %s missing", name)
		}
		err := errors.New(f.Raw)
		info := classifyOverflow(err)
		if info.Kind != KindBudget {
			t.Fatalf("%s: kind = %s, want budget: 133 input tokens fit, the 209990 reservation does not", name, info.Kind)
		}
		if isContextOverflow(err) {
			t.Fatalf("%s: a budget overflow must not trigger compaction", name)
		}
		if !isBudgetOverflow(err) {
			t.Fatalf("%s: isBudgetOverflow = false", name)
		}
		if w, ok := learnedWindowFrom(err); !ok || w != 210000 {
			t.Fatalf("%s: learned window = %d, %v, want 210000", name, w, ok)
		}
		needs, allows, ok := overflowFigures(err)
		if !ok || needs != 210123 || allows != 210000 {
			t.Fatalf("%s: overflowFigures = %d, %d, %v, want 210123, 210000", name, needs, allows, ok)
		}
	}
	for _, name := range []string{"regolo-vllm-output-cap-stream", "regolo-vllm-output-cap-nonstream"} {
		f := fx[name]
		err := errors.New(f.Raw)
		if got := classifyOverflow(err); got.Kind != KindOutputCap || got.Output != 250000 || got.Window != 210000 {
			t.Fatalf("%s: %+v, want output_cap 250000 / 210000", name, got)
		}
		if isContextOverflow(err) {
			t.Fatalf("%s: an output-cap error is not a context overflow", name)
		}
		if !isOutputCapError(err) {
			t.Fatalf("%s: isOutputCapError = false", name)
		}
		if w, ok := learnedWindowFrom(err); ok {
			t.Fatalf("%s: learned window %d from an output-cap error", name, w)
		}
	}
}

// Budget versus Context is decided from the figures: the same row with an
// input that alone fills the window is a context overflow.
func TestClassifyOverflowBudgetVersusContext(t *testing.T) {
	budget := errors.New("Requested token count exceeds the model's maximum context length of 210000 tokens. You requested a total of 210123 tokens: 133 tokens from the input messages and 209990 tokens for the completion.")
	if got := classifyOverflow(budget); got.Kind != KindBudget {
		t.Fatalf("kind = %s, want budget", got.Kind)
	}
	ctx := errors.New("Requested token count exceeds the model's maximum context length of 210000 tokens. You requested a total of 212000 tokens: 210000 tokens from the input messages and 2000 tokens for the completion.")
	if got := classifyOverflow(ctx); got.Kind != KindContext {
		t.Fatalf("kind = %s, want context: the prompt alone does not fit", got.Kind)
	}
	if !isContextOverflow(ctx) {
		t.Fatal("isContextOverflow = false for a prompt that alone fills the window")
	}
}

// The status gate applies to weak evidence only. A specific provider row is
// strong and classifies at any status: LocalAI and llama.cpp report an
// overflow as HTTP 500 over gRPC. A generic phrase counts only with 400, 413
// or an unknown status.
func TestClassifyOverflowStatusGate(t *testing.T) {
	const localai = "rpc error: code = Internal desc = request (9739 tokens) exceeds the available context size (8192 tokens), try increasing it"
	strong := []struct {
		name string
		err  error
	}{
		{"localai stream text", errors.New("localai stream: status 500: " + localai)},
		{"localai typed stream text", errors.New("localai stream: error, status code: 500, status: 500 Internal Server Error, message: " + localai)},
		{"RequestError", fmt.Errorf("wrapped: %w", &openai.RequestError{HTTPStatusCode: 500, Err: errors.New(localai)})},
		{"APIError", fmt.Errorf("wrapped: %w", &openai.APIError{HTTPStatusCode: 500, Message: localai})},
	}
	for _, tc := range strong {
		t.Run("strong 500 "+tc.name, func(t *testing.T) {
			got := classifyOverflow(tc.err)
			if got.Kind != KindContext || got.Status != 500 || got.Window != 8192 || got.Input != 9739 {
				t.Fatalf("%+v, want context with status 500, window 8192, input 9739", got)
			}
			if !isContextOverflow(tc.err) {
				t.Fatal("isContextOverflow = false for a LocalAI overflow reported as HTTP 500")
			}
			if classifyBackendError(tc.err) != errFatal {
				t.Fatal("a LocalAI overflow reported as HTTP 500 must not be retried")
			}
		})
	}
	for _, status := range []int{429, 503, 401} {
		err := fmt.Errorf("localai stream: status %d: %s", status, localai)
		if got := classifyOverflow(err); got.Kind != KindContext || got.Window != 8192 {
			t.Fatalf("strong row, status %d: %+v, want context", status, got)
		}
	}

	const generic = "context window exceeded"
	for _, status := range []int{500, 429, 401, 503} {
		err := fmt.Errorf("localai stream: status %d: %s", status, generic)
		if got := classifyOverflow(err); got.Kind != KindNone {
			t.Fatalf("generic row, status %d: kind = %s (pattern %q), want none", status, got.Kind, got.Pattern)
		}
		if got := classifyOverflow(&openai.RequestError{HTTPStatusCode: status, Err: errors.New(generic)}); got.Kind != KindNone {
			t.Fatalf("generic row, RequestError %d: kind = %s, want none", status, got.Kind)
		}
		if got := classifyOverflow(&openai.APIError{HTTPStatusCode: status, Message: generic}); got.Kind != KindNone {
			t.Fatalf("generic row, APIError %d: kind = %s, want none", status, got.Kind)
		}
	}
	if got := classifyOverflow(errors.New(generic)); got.Kind != KindContext || got.Status != 0 {
		t.Fatalf("generic row, no status (stream error chunk): %+v, want context with status 0", got)
	}

	cases := []struct {
		name string
		err  error
	}{
		{"RequestError", fmt.Errorf("wrapped: %w", &openai.RequestError{HTTPStatusCode: 400, Err: errors.New(generic)})},
		{"APIError", fmt.Errorf("wrapped: %w", &openai.APIError{HTTPStatusCode: 400, Message: generic})},
		{"localai stream text", errors.New("localai stream: status 400: " + generic)},
		{"localai typed stream text", errors.New("localai stream: error, status code: 400, status: 400 Bad Request, message: " + generic)},
		{"openai sdk text", errors.New("error, status code: 400, status: 400 Bad Request, message: " + generic)},
	}
	for _, tc := range cases {
		t.Run("generic 400 "+tc.name, func(t *testing.T) {
			if got := errorStatus(tc.err); got != 400 {
				t.Fatalf("errorStatus = %d, want 400", got)
			}
			if got := classifyOverflow(tc.err); got.Kind != KindContext || got.Status != 400 {
				t.Fatalf("%+v, want context with status 400", got)
			}
		})
	}
	if got := errorStatus(errors.New("dial tcp: connection refused")); got != 0 {
		t.Fatalf("errorStatus of a status-less error = %d, want 0", got)
	}
	if got := errorStatus(nil); got != 0 {
		t.Fatalf("errorStatus(nil) = %d", got)
	}
}

// Every built-in row says whether it is strong or weak evidence, and only the
// generic phrases are weak.
func TestOverflowPatternStrength(t *testing.T) {
	for _, p := range builtinOverflowPatterns {
		generic := strings.HasPrefix(p.name, "generic")
		if p.strong == generic {
			t.Fatalf("row %q: strong = %v, want %v", p.name, p.strong, !generic)
		}
	}
}

// A structured error code decides the kind even when no row knows the text.
func TestClassifyOverflowStructuredCode(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &openai.APIError{Code: "context_length_exceeded", Message: "your request is too big for this model"})
	if got := classifyOverflow(err); got.Kind != KindContext {
		t.Fatalf("kind = %s, want context", got.Kind)
	}
	err = &openai.APIError{Code: "max_tokens_exceeded", Message: "the reservation does not fit"}
	if got := classifyOverflow(err); got.Kind != KindBudget {
		t.Fatalf("kind = %s, want budget", got.Kind)
	}
	// The rows still run for the figures.
	err = &openai.APIError{Code: "context_length_exceeded", Message: "This model's maximum context length is 8192 tokens. However, your messages resulted in 9739 tokens."}
	if got := classifyOverflow(err); got.Kind != KindContext || got.Window != 8192 || got.Input != 9739 {
		t.Fatalf("%+v, want context with window 8192, input 9739", got)
	}
}

// The output-cap guard: a max_tokens validation error is not an overflow.
func TestClassifyOverflowMaxTokensValidation(t *testing.T) {
	for _, msg := range []string{
		"Invalid 'max_tokens': integer above maximum value",
		"max_tokens must be less than the context length",
		"max_completion_tokens: exceeds the context window",
	} {
		if got := classifyOverflow(errors.New(msg)); got.Kind != KindNone {
			t.Fatalf("%q: kind = %s (pattern %q), want none", msg, got.Kind, got.Pattern)
		}
	}
}

// A 413 whose text names no tokens may be a byte or media limit, which
// compaction cannot fix: it is an overflow only when the last request was
// near the window.
func TestClassifyOverflow413WithoutTokenWording(t *testing.T) {
	err := &openai.RequestError{HTTPStatusCode: 413, Err: errors.New("Request Entity Too Large")}
	if got := classifyOverflow(err); got.Kind != KindNone {
		t.Fatalf("no estimate: kind = %s, want none", got.Kind)
	}
	if got := classifyOverflowWith(err, 7000, 10000); got.Kind != KindNone {
		t.Fatalf("70%% of the window: kind = %s, want none", got.Kind)
	}
	if got := classifyOverflowWith(err, 8000, 10000); got.Kind != KindContext || got.Status != 413 {
		t.Fatalf("80%% of the window: %+v, want context with status 413", got)
	}
	// A 413 with token wording is classified by its row, estimate or not.
	worded := &openai.RequestError{HTTPStatusCode: 413, Err: errors.New("prompt is too long: 210000 tokens > 200000 maximum")}
	if got := classifyOverflow(worded); got.Kind != KindContext || got.Window != 200000 {
		t.Fatalf("%+v, want context with window 200000", got)
	}
}

// Config patterns are tried before the built-ins; an invalid regex is skipped.
func TestOverflowPatternConfigPrecedence(t *testing.T) {
	t.Cleanup(func() { setConfigOverflowPatterns(nil) })
	msg := errors.New("request (9739 tokens) exceeds the available context size (8192 tokens), try increasing it")
	if got := classifyOverflow(msg); got.Pattern != "llamacpp" {
		t.Fatalf("built-in pattern = %q, want llamacpp", got.Pattern)
	}
	setConfigOverflowPatterns([]types.OverflowPattern{
		{Name: "broken", Kind: "context", Regex: `(unclosed`},
		{Name: "bad-kind", Kind: "sideways", Regex: `request`},
		{Name: "mine", Kind: "context", Regex: `request \((?P<input>\d+) tokens\) exceeds the available context size \((?P<window>\d+) tokens\)`},
		{Name: "new-backend", Kind: "context", Regex: `prompt of (?P<input>\d+) is over (?P<window>\d+)`},
	})
	got := classifyOverflow(msg)
	if got.Pattern != "mine" || got.Window != 8192 || got.Input != 9739 {
		t.Fatalf("%+v, want the config row \"mine\" with its figures", got)
	}
	if got := classifyOverflow(errors.New("prompt of 5000 is over 4096")); got.Kind != KindContext || got.Pattern != "new-backend" || got.Window != 4096 {
		t.Fatalf("%+v, want the config row for a wording nib does not know", got)
	}
	if got := classifyOverflow(errors.New("request timed out")); got.Kind != KindNone {
		t.Fatalf("an invalid-kind row matched: %+v", got)
	}
}

// A generic-row match is an overflow with no figures, and no window is
// learned from it.
func TestClassifyOverflowGenericRow(t *testing.T) {
	err := errors.New("the prompt exceeds the context window of this deployment")
	got := classifyOverflow(err)
	if got.Kind != KindContext || !strings.HasPrefix(got.Pattern, "generic") {
		t.Fatalf("%+v, want a generic context match", got)
	}
	if got.Window != 0 || got.Total != 0 || got.Input != 0 || got.Output != 0 {
		t.Fatalf("a generic row stated figures: %+v", got)
	}
	if w, ok := learnedWindowFrom(err); ok {
		t.Fatalf("learned window %d from a generic row", w)
	}
	if _, _, ok := overflowFigures(err); ok {
		t.Fatal("overflowFigures reported figures for a generic row")
	}
}

// A figure-carrying row nested under a humanized FriendlyError still wins
// over the generic wording of the outer message.
func TestClassifyOverflowPrefersFiguresInTheChain(t *testing.T) {
	raw := errors.New("Requested token count exceeds the model's maximum context length of 210000 tokens. You requested a total of 210123 tokens: 133 tokens from the input messages and 209990 tokens for the completion.")
	humanized := humanizeError(raw)
	if humanized == raw {
		t.Fatal("a budget overflow was not humanized")
	}
	if got := classifyOverflow(humanized); got.Kind != KindBudget || got.Window != 210000 {
		t.Fatalf("%+v, want the budget figures from the wrapped error", got)
	}
}
