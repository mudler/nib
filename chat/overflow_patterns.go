package chat

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
	openai "github.com/sashabaranov/go-openai"
)

// OverflowKind says what a backend rejection means for recovery.
type OverflowKind int

const (
	// KindNone: the error is not about size.
	KindNone OverflowKind = iota
	// KindContext: the prompt itself does not fit the window. Compact.
	KindContext
	// KindBudget: the prompt fits, but prompt + requested output does not.
	// Lower max_tokens and retry. Never compact.
	KindBudget
	// KindOutputCap: the requested output alone is above the model's maximum.
	// Lower the output cap. Do not compact.
	KindOutputCap
)

// String returns the name fixtures and config use for the kind.
func (k OverflowKind) String() string {
	switch k {
	case KindContext:
		return "context"
	case KindBudget:
		return "budget"
	case KindOutputCap:
		return "output_cap"
	default:
		return "none"
	}
}

func parseOverflowKind(s string) (OverflowKind, bool) {
	switch s {
	case "context":
		return KindContext, true
	case "budget":
		return KindBudget, true
	case "output_cap":
		return KindOutputCap, true
	}
	return KindNone, false
}

// overflowInfo is what classifyOverflow read from an error. A zero figure
// means the message did not state it.
type overflowInfo struct {
	Kind    OverflowKind
	Pattern string
	Status  int
	Window  int
	Total   int
	Input   int
	Output  int
}

// hasFigures reports whether the match stated any number at all.
func (o overflowInfo) hasFigures() bool {
	return o.Window > 0 || o.Total > 0 || o.Input > 0 || o.Output > 0
}

// overflowPattern is one row of the catalog. Figures come from the named
// capture groups window, total, input and output, so a row states what each
// number means instead of relying on the order the backend printed them.
type overflowPattern struct {
	name   string
	kind   OverflowKind
	re     *regexp.Regexp
	strong bool
}

// Evidence strength of a row, for the status gate (see classifyOverflowWith).
// A strong row (a structured code or a specific provider wording) classifies
// at any HTTP status; a weak row (a generic phrase) only with 400, 413 or an
// unknown status.
const (
	strongEvidence = true
	weakEvidence   = false
)

// builtinOverflowPatterns are tried in order, most specific first; the first
// match wins. Each row with figures is backed by a fixture in
// testdata/overflow. The generic phrases at the end detect an overflow in a
// wording no row names, but state no figures, so no window is learned from
// them and the summary retry falls back to halving.
var builtinOverflowPatterns = []overflowPattern{
	// vLLM, newest (seen through regolo).
	{"vllm_total", KindContext, regexp.MustCompile(`maximum context length of (?P<window>\d+) tokens\. You requested a total of (?P<total>\d+) tokens: (?P<input>\d+) tokens from the input messages and (?P<output>\d+) tokens for the completion`), strongEvidence},
	// vLLM, current (vllm/renderers/params.py).
	{"vllm_current", KindContext, regexp.MustCompile(`maximum context length is (?P<window>\d+) tokens\. However, you requested (?P<output>\d+) output tokens and your prompt contains (?P<input>\d+) input tokens, for a total of (?P<total>\d+) tokens`), strongEvidence},
	// vLLM, older OpenAI-compatible server.
	{"vllm_older", KindContext, regexp.MustCompile(`maximum context length is (?P<window>\d+) tokens\. However, you requested (?P<total>\d+) tokens \((?P<input>\d+) in the messages, (?P<output>\d+) in the completion\)`), strongEvidence},
	// Anthropic, prompt + max_tokens.
	{"anthropic_budget", KindBudget, regexp.MustCompile("input length and `?max_tokens`? exceed context limit: (?P<input>\\d+) \\+ (?P<output>\\d+) > (?P<window>\\d+)"), strongEvidence},
	// vLLM output cap (seen through regolo; note no space after the period).
	{"vllm_output_cap", KindOutputCap, regexp.MustCompile(`max_(?:completion_)?tokens is too large: (?P<output>\d+)\.\s*This model supports at most (?P<window>\d+) completion tokens`), strongEvidence},
	// OpenAI.
	{"openai", KindContext, regexp.MustCompile(`maximum context length is (?P<window>\d+) tokens\. However, your messages resulted in (?P<input>\d+) tokens`), strongEvidence},
	// llama.cpp / LocalAI (tools/server/server-context.cpp).
	{"llamacpp", KindContext, regexp.MustCompile(`request \((?P<input>\d+) tokens\) exceeds the available context size \((?P<window>\d+) tokens\)`), strongEvidence},
	// Anthropic, prompt alone.
	{"anthropic", KindContext, regexp.MustCompile(`prompt is too long: (?P<input>\d+) tokens > (?P<window>\d+) maximum`), strongEvidence},
	// Gemini / Vertex.
	{"gemini", KindContext, regexp.MustCompile(`(?i)input token count \((?P<input>\d+)\) exceeds the maximum number of tokens allowed \((?P<window>\d+)\)`), strongEvidence},
	// Bare codes.
	{"code_context_length_exceeded", KindContext, regexp.MustCompile(`context_length_exceeded|ContextWindowExceededError`), strongEvidence},
	// Generic phrases: detection only, no figures.
	{"generic_available_context_size", KindContext, regexp.MustCompile(`(?i)exceeds the available context size`), weakEvidence},
	{"generic_maximum_context_length", KindContext, regexp.MustCompile(`(?i)maximum context length`), weakEvidence},
	{"generic_context_window", KindContext, regexp.MustCompile(`(?i)context window`), weakEvidence},
	{"generic_context_size", KindContext, regexp.MustCompile(`(?i)context size`), weakEvidence},
	{"generic_context_length", KindContext, regexp.MustCompile(`(?i)context length`), weakEvidence},
}

// configOverflowPatterns holds the compiled compaction.overflow_patterns rows.
// It is package state rather than a session field because classifyOverflow is
// a free function called from paths that hold no session (humanizeError,
// classifyBackendError); every session of one process loads the same config.
var configOverflowPatterns atomic.Pointer[[]overflowPattern]

// setConfigOverflowPatterns compiles the user's rows. An invalid regex or kind
// is logged and skipped; it never fails the session.
func setConfigOverflowPatterns(in []types.OverflowPattern) {
	var rows []overflowPattern
	for _, p := range in {
		kind, ok := parseOverflowKind(p.Kind)
		if !ok {
			xlog.Warn("overflow pattern skipped: unknown kind", "name", p.Name, "kind", p.Kind)
			continue
		}
		re, err := regexp.Compile(p.Regex)
		if err != nil {
			xlog.Warn("overflow pattern skipped: invalid regex", "name", p.Name, "error", err)
			continue
		}
		name := p.Name
		if name == "" {
			name = "config"
		}
		// A config row names one backend's wording: strong evidence.
		rows = append(rows, overflowPattern{name: name, kind: kind, re: re, strong: strongEvidence})
	}
	configOverflowPatterns.Store(&rows)
}

func overflowRows() []overflowPattern {
	if p := configOverflowPatterns.Load(); p != nil && len(*p) > 0 {
		return append(append([]overflowPattern(nil), *p...), builtinOverflowPatterns...)
	}
	return builtinOverflowPatterns
}

// errorStatus returns the HTTP status carried by the error chain, or 0 when
// unknown. The typed go-openai errors are read first; then the text, because
// cogito's streaming path flattens the status into "localai stream: status
// 400: <body>" and the OpenAI SDK prints "status code: 400".
func errorStatus(err error) int {
	if err == nil {
		return 0
	}
	var re *openai.RequestError
	if errors.As(err, &re) && re.HTTPStatusCode > 0 {
		return re.HTTPStatusCode
	}
	var ae *openai.APIError
	if errors.As(err, &ae) && ae.HTTPStatusCode > 0 {
		return ae.HTTPStatusCode
	}
	if m := statusRe.FindStringSubmatch(strings.ToLower(err.Error())); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// classifyOverflow reports what kind of size rejection err is, with the
// figures the backend stated. It knows nothing about the request that failed,
// so a 413 without token wording is KindNone (see classifyOverflowWith).
func classifyOverflow(err error) overflowInfo {
	return classifyOverflowWith(err, 0, 0)
}

// classifyOverflowWith is classifyOverflow with the local estimate of the last
// request and the window it was sent against. They decide only one case: a 413
// whose text names no tokens, which is an overflow when the request was at
// least 80% of the window and otherwise a byte or media limit that compaction
// cannot fix. Zeros mean "unknown", and such a 413 is KindNone.
//
// The status gate applies to weak evidence only: a generic phrase row and the
// 413 rule count when the status is 400, 413 or unknown (0: an error chunk
// mid-stream carries none, and that must not disable recovery). Strong
// evidence (a structured code, a specific provider row, a config row)
// classifies at any status: LocalAI and llama.cpp report an overflow as HTTP
// 500, and proxies rewrap statuses.
func classifyOverflowWith(err error, lastRequestTokens, window int) overflowInfo {
	if err == nil {
		return overflowInfo{}
	}
	status := errorStatus(err)
	weakOK := status == 0 || status == 400 || status == 413
	m := matchOverflow(err, weakOK)
	m.Status = status
	if m.Kind != KindNone {
		return m
	}
	// e. 413 without token wording (weak evidence; the status is 413 here, so
	// the gate always admits it).
	if status == 413 {
		if window > 0 && lastRequestTokens > 0 && float64(lastRequestTokens) >= 0.8*float64(window) {
			return overflowInfo{Kind: KindContext, Pattern: "http_413", Status: status}
		}
		xlog.Debug("HTTP 413 not classified as a context overflow: no token wording and the last request was not near the window",
			"error", err.Error(), "last_request_tokens", lastRequestTokens, "window", window)
	}
	return overflowInfo{Status: status}
}

// matchOverflow applies steps b-d (structured code, regex rows, output-cap
// guard) to err. weakOK says whether the status gate admits weak rows.
func matchOverflow(err error, weakOK bool) overflowInfo {
	var info overflowInfo
	if err == nil {
		return info
	}
	// b. Structured error code: strong evidence.
	var ae *openai.APIError
	if errors.As(err, &ae) {
		if code, ok := ae.Code.(string); ok {
			switch code {
			case "context_length_exceeded":
				info = overflowInfo{Kind: KindContext, Pattern: "code:" + code}
			case "max_tokens_exceeded":
				info = overflowInfo{Kind: KindBudget, Pattern: "code:" + code}
			}
		}
	}
	// c. Regex rows, over every level of the chain: humanizeError wraps the
	// backend error in a FriendlyError whose own text keeps the generic
	// wording but not every figure. A row with figures wins over one without,
	// and a strong row over a weak one.
	var row overflowInfo
	rowStrong := false
	for e := err; e != nil; e = errors.Unwrap(e) {
		r, strong := matchRows(e.Error(), weakOK)
		if r.Kind == KindNone {
			continue
		}
		if row.Kind == KindNone || (strong && !rowStrong) {
			row, rowStrong = r, strong
		}
		if r.hasFigures() {
			row, rowStrong = r, strong
			break
		}
	}
	if info.Kind != KindNone {
		// The code decides the kind; the rows only add figures.
		if row.hasFigures() {
			info.Window, info.Total, info.Input, info.Output = row.Window, row.Total, row.Input, row.Output
		}
		return info
	}
	// d. Output-cap guard: a message naming the output parameter that no row
	// with figures explained is an invalid-parameter error, not an overflow.
	if !row.hasFigures() && namesOutputParam(err.Error()) {
		return overflowInfo{}
	}
	return row
}

var outputParamRe = regexp.MustCompile(`(?i)max_(?:completion_|output_)?tokens`)

func namesOutputParam(msg string) bool { return outputParamRe.MatchString(msg) }

// matchRows returns the first row matching msg, with its figures, the kind
// they imply and whether the row is strong evidence. Weak rows are skipped
// unless weakOK.
func matchRows(msg string, weakOK bool) (overflowInfo, bool) {
	for _, p := range overflowRows() {
		if !p.strong && !weakOK {
			continue
		}
		sub := p.re.FindStringSubmatch(msg)
		if sub == nil {
			continue
		}
		info := overflowInfo{Kind: p.kind, Pattern: p.name}
		for i, g := range p.re.SubexpNames() {
			if g == "" || sub[i] == "" {
				continue
			}
			n, err := strconv.Atoi(sub[i])
			if err != nil {
				continue
			}
			switch g {
			case "window":
				info.Window = n
			case "total":
				info.Total = n
			case "input":
				info.Input = n
			case "output":
				info.Output = n
			}
		}
		// Budget vs Context comes from the figures, not the row: when the
		// input fits the window and output is reserved, lowering the output
		// fixes it; otherwise the prompt alone does not fit.
		if p.kind != KindOutputCap && info.Input > 0 && info.Output > 0 && info.Window > 0 {
			if info.Input < info.Window {
				info.Kind = KindBudget
			} else {
				info.Kind = KindContext
			}
		}
		return info, p.strong
	}
	return overflowInfo{}, false
}

// isBudgetOverflow reports whether err says the prompt fits but prompt plus
// the requested output does not.
func isBudgetOverflow(err error) bool {
	return classifyOverflow(err).Kind == KindBudget
}

// isOutputCapError reports whether err says the requested output alone is
// above the model's maximum.
func isOutputCapError(err error) bool {
	return classifyOverflow(err).Kind == KindOutputCap
}

// logUnclassifiedRejection logs a 400/413 that no pattern recognised, so an
// unknown overflow wording can be turned into a pattern.
func logUnclassifiedRejection(err error, info overflowInfo) {
	if err == nil || info.Kind != KindNone {
		return
	}
	if s := errorStatus(err); s == 400 || s == 413 {
		xlog.Debug("request rejected; if this is a context overflow, add a pattern (see compaction.overflow_patterns)",
			"status", s, "error", err.Error())
	}
}

// classifyTurnOverflow is classifyOverflowWith for the turn that just failed:
// the last request's size is the in-flight usage figure, or the conversation's
// estimate when the backend has reported none (ContextTokens), measured
// against the window the session budgets for.
func (s *Session) classifyTurnOverflow(err error) overflowInfo {
	return classifyOverflowWith(err, s.ContextTokens(), s.contextWindow())
}
