package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
	openai "github.com/sashabaranov/go-openai"
)

func numberedLines(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	return strings.Join(lines, "\n")
}

func TestProgressiveCompressFullUnchanged(t *testing.T) {
	in := numberedLines(100)
	if got := compressToolOutput(in, "a.go", CompressionFull, nil); got != in {
		t.Fatalf("full level changed the content")
	}
	if got := compressToolOutput(compressToolOutput(in, "", CompressionFull, nil), "", CompressionFull, nil); got != in {
		t.Fatalf("full level not idempotent")
	}
}

func TestProgressiveCompressTruncated(t *testing.T) {
	in := numberedLines(100)
	got := compressToolOutput(in, "", CompressionTruncated, nil)
	lines := strings.Split(got, "\n")
	// 20 head + marker + 10 tail
	if len(lines) != 31 {
		t.Fatalf("got %d lines, want 31:\n%s", len(lines), got)
	}
	if lines[0] != "line 1" || lines[19] != "line 20" {
		t.Fatalf("head wrong: %q .. %q", lines[0], lines[19])
	}
	if lines[20] != "[...70 lines elided...]" {
		t.Fatalf("marker wrong: %q", lines[20])
	}
	if lines[21] != "line 91" || lines[30] != "line 100" {
		t.Fatalf("tail wrong: %q .. %q", lines[21], lines[30])
	}
	// Short content has nothing to elide.
	short := numberedLines(30)
	if got := compressToolOutput(short, "", CompressionTruncated, nil); got != short {
		t.Fatalf("short content changed: %q", got)
	}
}

func TestProgressiveCompressOutline(t *testing.T) {
	in := numberedLines(100)
	called := ""
	outlineOf := func(p string) string { called = p; return "func Foo [1-10]" }
	got := compressToolOutput(in, "a.go", CompressionOutline, outlineOf)
	if called != "a.go" {
		t.Fatalf("outline function not called with the path, got %q", called)
	}
	if !strings.Contains(got, "func Foo [1-10]") || strings.Contains(got, "line 50") {
		t.Fatalf("outline level did not use the outline: %q", got)
	}

	// No outline function (not a read/index result): first + last line.
	got = compressToolOutput(in, "", CompressionOutline, nil)
	want := "line 1\n[...elided...]\nline 100"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	// Outline unavailable: fall back to first + last line.
	got = compressToolOutput(in, "a.go", CompressionOutline, func(string) string { return "" })
	if got != want {
		t.Fatalf("fallback got %q, want %q", got, want)
	}
}

func TestProgressiveCompressElided(t *testing.T) {
	in := "head\n... output spilled; full content at artifact://7 ...\ntail"
	got := compressToolOutput(in, "", CompressionElided, nil)
	if got != "[output elided — see artifact://7 for full content]" {
		t.Fatalf("got %q", got)
	}
	if again := compressToolOutput(got, "", CompressionElided, nil); again != got {
		t.Fatalf("elided not idempotent: %q -> %q", got, again)
	}

	got = compressToolOutput(numberedLines(50), "a.go", CompressionElided, nil)
	if strings.Contains(got, "artifact://") || !strings.Contains(got, "elided") || !strings.Contains(got, "a.go") {
		t.Fatalf("no-artifact stub wrong: %q", got)
	}
	if again := compressToolOutput(got, "a.go", CompressionElided, nil); again != got {
		t.Fatalf("elided not idempotent: %q -> %q", got, again)
	}
}

// progressiveSession builds a session whose budget is window-reserve tokens.
func progressiveSession(window, keep int) *Session {
	return &Session{
		compaction: types.CompactionConfig{KeepRecent: keep, MaxContextTokens: window, ReserveTokens: 1, Threshold: 0.8},
		// Size pruning off: only the progressive levels are under test.
		pruning: types.ToolOutputPruningConfig{DisableStaleReads: true},
	}
}

// progressiveHistory is n exchanges of a bash call + 100-line result, then a
// user message. Results sit at indices 1, 3, 5, ...
func progressiveHistory(n int) []openai.ChatCompletionMessage {
	var msgs []openai.ChatCompletionMessage
	for i := range n {
		id := fmt.Sprintf("c%d", i)
		msgs = append(msgs, callMsg(id, "bash", `{"command":"ls"}`), resultMsg(id, numberedLines(100)))
	}
	return append(msgs, openai.ChatCompletionMessage{Role: "user", Content: "next"})
}

func levelsOf(t *testing.T, in, out []openai.ChatCompletionMessage) []string {
	t.Helper()
	var got []string
	for i := range out {
		if out[i].Role != "tool" {
			continue
		}
		switch {
		case out[i].Content == in[i].Content:
			got = append(got, "full")
		case strings.Contains(out[i].Content, "output elided"):
			got = append(got, "elided")
		case strings.Contains(out[i].Content, "[...elided...]"):
			got = append(got, "outline")
		case strings.Contains(out[i].Content, "lines elided...]"):
			got = append(got, "truncated")
		default:
			got = append(got, "?")
		}
	}
	return got
}

func checkPairing(t *testing.T, in, out []openai.ChatCompletionMessage) {
	t.Helper()
	if len(in) != len(out) {
		t.Fatalf("message count changed %d -> %d", len(in), len(out))
	}
	for i := range in {
		if in[i].Role != out[i].Role || in[i].ToolCallID != out[i].ToolCallID || len(in[i].ToolCalls) != len(out[i].ToolCalls) {
			t.Fatalf("message %d changed shape", i)
		}
	}
}

func TestProgressiveCompressAssignment(t *testing.T) {
	msgs := progressiveHistory(9) // 19 messages, ~6.3k tokens
	// ~60% pressure: positional levels, no escalation.
	s := progressiveSession(estimateTokens(msgs)*10/6, 4)
	out := s.progressivePrune(msgs)
	checkPairing(t, msgs, out)
	got := strings.Join(levelsOf(t, msgs, out), ",")
	// indices 1,3,5 < 19/3=6 → outline; 7,9,11 < 12 → truncated; 13 → full
	// (last third); 15,17 in the last 4 → full.
	want := "outline,outline,outline,truncated,truncated,truncated,full,full,full"
	if got != want {
		t.Fatalf("levels\n got %s\nwant %s", got, want)
	}
}

func TestProgressiveCompressLowPressureLeavesAlone(t *testing.T) {
	msgs := progressiveHistory(9)
	s := progressiveSession(estimateTokens(msgs)*10, 4)
	out := s.progressivePrune(msgs)
	for i := range msgs {
		if out[i].Content != msgs[i].Content {
			t.Fatalf("message %d compressed at low pressure", i)
		}
	}
}

func TestProgressiveCompressPressureEscalation(t *testing.T) {
	msgs := progressiveHistory(9)
	s := progressiveSession(estimateTokens(msgs)*100/85, 4)
	out := s.progressivePrune(msgs)
	checkPairing(t, msgs, out)
	got := strings.Join(levelsOf(t, msgs, out), ",")
	want := "elided,elided,elided,outline,outline,outline,full,full,full"
	if got != want {
		t.Fatalf("80%% levels\n got %s\nwant %s", got, want)
	}

	s = progressiveSession(estimateTokens(msgs)*100/95, 4)
	out = s.progressivePrune(msgs)
	checkPairing(t, msgs, out)
	got = strings.Join(levelsOf(t, msgs, out), ",")
	want = "elided,elided,elided,elided,elided,elided,elided,full,full"
	if got != want {
		t.Fatalf("90%% levels\n got %s\nwant %s", got, want)
	}
}

func TestProgressiveCompressReadUsesOutline(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("r1", "read", `{"path":"missing-file.go"}`), resultMsg("r1", numberedLines(100)),
		callMsg("b1", "bash", `{}`), resultMsg("b1", numberedLines(100)),
		callMsg("b2", "bash", `{}`), resultMsg("b2", numberedLines(100)),
		{Role: "user", Content: "next"},
	}
	s := progressiveSession(estimateTokens(msgs)*10/6, 2)
	out := s.progressivePrune(msgs)
	// missing-file.go cannot be indexed, so the read falls back to first+last.
	if out[1].Content != "line 1\n[...elided...]\nline 100" {
		t.Fatalf("read result: %q", out[1].Content)
	}
}

// Stickiness: once a result is compressed, dropping pressure does not bring it
// back, and consecutive requests below the next threshold keep the prefix.
func TestProgressiveCompressSticky(t *testing.T) {
	msgs := progressiveHistory(9)
	s := progressiveSession(estimateTokens(msgs)*100/85, 4)
	first := s.progressivePrune(msgs)

	// Pressure drops well below every threshold (bigger window).
	s.compaction.MaxContextTokens = estimateTokens(msgs) * 10
	second := s.progressivePrune(msgs)
	for i := range first {
		if first[i].Content != second[i].Content {
			t.Fatalf("message %d changed after pressure dropped:\n%q\n->\n%q", i, first[i].Content, second[i].Content)
		}
	}

	// Growing within the same band: the existing prefix is byte-identical, and
	// newly old results are not compressed one per request.
	s.compaction.MaxContextTokens = estimateTokens(msgs) * 100 / 55
	third := s.progressivePrune(msgs)
	grown := append(append([]openai.ChatCompletionMessage{}, msgs...),
		callMsg("n1", "bash", `{}`), resultMsg("n1", numberedLines(100)),
		openai.ChatCompletionMessage{Role: "user", Content: "more"})
	fourth := s.progressivePrune(grown)
	for i := range third {
		if third[i].Content != fourth[i].Content {
			t.Fatalf("prefix message %d changed between requests in the same band", i)
		}
	}

	// Compaction's view renders the same levels the requests saw.
	s.prunedMu.Lock()
	view := s.compressedViewLocked(grown)
	s.prunedMu.Unlock()
	for i := range fourth {
		if view[i].Content != fourth[i].Content {
			t.Fatalf("compaction view differs at %d", i)
		}
	}
}

// A result already stubbed by the size sweep keeps its stub bytes.
func TestProgressiveCompressKeepsExistingStubs(t *testing.T) {
	msgs := progressiveHistory(9)
	s := progressiveSession(estimateTokens(msgs)*100/85, 4)
	s.prunedIDs = map[string]string{"c0": detailBudget}
	out := s.progressivePrune(msgs)
	if want := prunedStub("bash", "", detailBudget); out[1].Content != want {
		t.Fatalf("existing stub rewritten: %q", out[1].Content)
	}
}

func TestProgressiveCompressDisabled(t *testing.T) {
	msgs := progressiveHistory(9)
	s := progressiveSession(estimateTokens(msgs)*100/95, 4)
	s.pruning.Disabled = true
	out := s.progressivePrune(msgs)
	for i := range msgs {
		if out[i].Content != msgs[i].Content {
			t.Fatalf("message %d compressed with pruning disabled", i)
		}
	}
}

// With no artifact to point to, the elided stub progressivePrune renders names
// the tool that produced the output.
func TestProgressiveCompressElidedNamesTool(t *testing.T) {
	msgs := progressiveHistory(9)
	s := progressiveSession(estimateTokens(msgs)*100/95, 4)
	out := s.progressivePrune(msgs)
	if !strings.Contains(out[1].Content, "output elided") || !strings.Contains(out[1].Content, "bash") {
		t.Fatalf("elided stub does not name the tool: %q", out[1].Content)
	}
	if strings.Contains(out[1].Content, "artifact://") {
		t.Fatalf("elided stub names an artifact that does not exist: %q", out[1].Content)
	}
}

// The real request path compresses: a turn's manipulator applies the levels,
// and two consecutive requests in the same band send byte-identical prefixes.
func TestProgressiveCompressThroughManipulate(t *testing.T) {
	msgs := progressiveHistory(9)
	s := progressiveSession(estimateTokens(msgs)*10/6, 4)
	c := s.newTurnCompactor(context.Background())

	first := c.manipulate(msgs)
	checkPairing(t, msgs, first)
	got := strings.Join(levelsOf(t, msgs, first), ",")
	want := "outline,outline,outline,truncated,truncated,truncated,full,full,full"
	if got != want {
		t.Fatalf("manipulate levels\n got %s\nwant %s", got, want)
	}

	// The next step of the same turn: one more tool exchange, same band.
	grown := append(append([]openai.ChatCompletionMessage{}, msgs...),
		callMsg("n1", "bash", `{"command":"ls"}`), resultMsg("n1", "short"))
	second := c.manipulate(grown)
	for i := range first {
		if first[i].Content != second[i].Content {
			t.Fatalf("prefix message %d changed between consecutive requests:\n%q\n->\n%q", i, first[i].Content, second[i].Content)
		}
	}
	// The same request again is byte-identical as a whole.
	third := c.manipulate(grown)
	for i := range second {
		if second[i].Content != third[i].Content {
			t.Fatalf("message %d changed on a repeated request", i)
		}
	}
}

// Compaction summarizes what the requests sent: the recorded levels, not the
// raw fragment.
func TestProgressiveCompressCompactionViewMatchesRequests(t *testing.T) {
	var msgs []openai.ChatCompletionMessage
	for i := range 9 {
		id := fmt.Sprintf("c%d", i)
		lines := make([]string, 100)
		for j := range lines {
			lines[j] = fmt.Sprintf("%s-row-%d", id, j+1)
		}
		msgs = append(msgs, callMsg(id, "bash", `{"command":"ls"}`), resultMsg(id, strings.Join(lines, "\n")))
	}
	msgs = append(msgs, openai.ChatCompletionMessage{Role: "user", Content: "next"})

	llm := &fakeSummaryLLM{reply: "SUMMARY"}
	s := newCompactTestSession(llm, 4, msgs, append([]openai.ChatCompletionMessage(nil), msgs...))
	s.compaction.MaxContextTokens = estimateTokens(msgs) * 10 / 6
	s.compaction.ReserveTokens = 1
	s.pruning = types.ToolOutputPruningConfig{DisableStaleReads: true}

	sent := s.progressivePrune(msgs)
	if sent[1].Content == msgs[1].Content {
		t.Fatalf("setup: c0 not compressed")
	}
	if _, _, err := s.compactHistory(context.Background()); err != nil {
		t.Fatalf("compactHistory: %v", err)
	}
	var prompt strings.Builder
	for _, m := range llm.lastReq.Messages {
		prompt.WriteString(m.Content)
	}
	if strings.Contains(prompt.String(), "c0-row-50") {
		t.Fatalf("summary saw c0's raw body, which the requests never sent")
	}
	if !strings.Contains(prompt.String(), "c0-row-1") {
		t.Fatalf("summary did not see c0's compressed form")
	}
}

// The overflow recovery's forced prune measures the request as sent: a result
// the prune keeps, but a recorded level already compresses, counts at its
// compressed size.
func TestProgressiveCompressForcePruneMeasuresTheRequestView(t *testing.T) {
	frag := []openai.ChatCompletionMessage{{Role: "user", Content: "u1"}}
	frag = append(frag, toolExchange("a", strings.Repeat("a", 4*5000))...)
	frag = append(frag, toolExchange("b", numberedLines(500))...) // ~900 tokens
	frag = append(frag, toolExchange("c", "short")...)
	s := newCompactTestSession(&fakeSummaryLLM{}, 2, frag, nil)
	// b is under MinResultTokens, so the forced prune keeps it.
	s.pruning = types.ToolOutputPruningConfig{MinResultTokens: 2000}
	s.compressed = map[string]compressedResult{"b": {level: CompressionElided, content: "[bash — output elided; re-run or re-read if needed]"}}

	if err := s.forcePrune(500); err != nil {
		t.Fatalf("forcePrune measured the raw fragment, not the request: %v", err)
	}
}
