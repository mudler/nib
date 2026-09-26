package chat

import (
	"fmt"
	"regexp"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// CompressionLevel is how much of a tool result a request carries. Higher
// levels carry less. A result's level only ever goes up (see progressivePrune).
type CompressionLevel int

const (
	// CompressionFull is the original content.
	CompressionFull CompressionLevel = iota
	// CompressionTruncated keeps the first truncHeadLines and the last
	// truncTailLines lines, with a marker for the lines between.
	CompressionTruncated
	// CompressionOutline keeps the file outline for a read or index result,
	// and the first and last line for any other result.
	CompressionOutline
	// CompressionElided keeps a one-line stub that points to the artifact
	// holding the full output, when there is one.
	CompressionElided
)

const (
	truncHeadLines = 20
	truncTailLines = 10
)

// The pressure bands at which progressivePrune assigns levels. Below
// compressStartPressure nothing is compressed, so a short session pays no
// prefix-cache re-prefill.
const (
	compressStartPressure    = 0.5
	compressEscalatePressure = 0.8
	compressElidePressure    = 0.9
)

var artifactRefRe = regexp.MustCompile(`artifact://\d+`)

// compressToolOutput returns content compressed to level.
//
// path is the tool's path argument, or "". outlineOf returns the outline of a
// file, or "" when it has none; the caller passes nil for a result that is not
// a read or index result, and then the outline level keeps the first and last
// line.
//
// The result is a function of its arguments and of outlineOf alone, and the
// Full and Elided levels are idempotent.
func compressToolOutput(content string, path string, level CompressionLevel, outlineOf func(string) string) string {
	switch level {
	case CompressionTruncated:
		lines := strings.Split(content, "\n")
		if len(lines) <= truncHeadLines+truncTailLines {
			return content
		}
		elided := len(lines) - truncHeadLines - truncTailLines
		out := make([]string, 0, truncHeadLines+truncTailLines+1)
		out = append(out, lines[:truncHeadLines]...)
		out = append(out, fmt.Sprintf("[...%d lines elided...]", elided))
		out = append(out, lines[len(lines)-truncTailLines:]...)
		return strings.Join(out, "\n")
	case CompressionOutline:
		if path != "" && outlineOf != nil {
			if outline := capOutline(outlineOf(path)); outline != "" {
				stub := fmt.Sprintf("[outline of %s; read one part back with offset and limit]\n%s", path, outline)
				if len(stub) < len(content) {
					return stub
				}
			}
		}
		lines := strings.Split(content, "\n")
		if len(lines) <= 2 {
			return content
		}
		return lines[0] + "\n[...elided...]\n" + lines[len(lines)-1]
	case CompressionElided:
		return elidedStub("", path, content)
	default:
		return content
	}
}

// elidedStub is the CompressionElided text for content. It points to the
// artifact the content names, when there is one. Otherwise it names the tool
// and the path, so the model can tell what it lost and fetch it again.
//
// compressToolOutput has no tool name to give and passes "". progressivePrune
// knows the tool from the assistant message that issued the call, and passes
// it. The text is a function of its arguments alone, so it is idempotent.
func elidedStub(name, path, content string) string {
	if ref := artifactRefRe.FindString(content); ref != "" {
		return "[output elided — see " + ref + " for full content]"
	}
	what := strings.TrimSpace(name + " " + path)
	if what == "" {
		return "[output elided]"
	}
	return "[" + what + " — output elided; re-run or re-read if needed]"
}

// compressedResult is the level a tool result was compressed to and the text
// it was rendered as. The text is stored, not re-rendered: an outline is read
// from disk, and a file that changed would move the prompt prefix.
type compressedResult struct {
	level   CompressionLevel
	content string
}

// pressureBand maps context pressure to a band: 0 (no compression), 1
// (positional levels), 2 (escalated), 3 (everything old elided).
func pressureBand(pressure float64) int {
	switch {
	case pressure >= compressElidePressure:
		return 3
	case pressure >= compressEscalatePressure:
		return 2
	case pressure >= compressStartPressure:
		return 1
	}
	return 0
}

// assignLevel is the level the message at index i of n gets in band. The last
// keep messages are always Full.
func assignLevel(i, n, keep, band int) CompressionLevel {
	if band <= 0 || i >= n-keep {
		return CompressionFull
	}
	if band >= 3 {
		return CompressionElided
	}
	var lvl CompressionLevel
	switch {
	case i < n/3:
		lvl = CompressionOutline
	case i < 2*n/3:
		lvl = CompressionTruncated
	default:
		return CompressionFull
	}
	if band == 2 {
		lvl++
	}
	return lvl
}

// applyCompressed renders every recorded level onto out, in place. A result
// with a stub in pruned keeps its stub.
func applyCompressed(out []openai.ChatCompletionMessage, compressed map[string]compressedResult, pruned map[string]string) {
	if len(compressed) == 0 {
		return
	}
	for i := range out {
		if out[i].Role != "tool" {
			continue
		}
		c, ok := compressed[out[i].ToolCallID]
		if !ok {
			continue
		}
		if _, stubbed := pruned[out[i].ToolCallID]; stubbed {
			continue
		}
		out[i].Content = c.content
	}
}

// requestView returns a copy of msgs as a request renders them: the stubs in
// pruned, then the levels in compressed. Compaction and the overflow recovery
// measure and summarize this view, so they see what the requests saw.
func requestView(msgs []openai.ChatCompletionMessage, pruned map[string]string, compressed map[string]compressedResult) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, len(msgs))
	copy(out, stubbedView(msgs, pruned))
	applyCompressed(out, compressed, pruned)
	return out
}

// compressedViewLocked is requestView with the session's recorded stubs and
// levels. Callers hold prunedMu.
func (s *Session) compressedViewLocked(msgs []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	return requestView(msgs, s.prunedIDs, s.compressed)
}

// copyCompressedLocked returns a copy of s.compressed. Callers hold prunedMu.
func (s *Session) copyCompressedLocked() map[string]compressedResult {
	out := make(map[string]compressedResult, len(s.compressed))
	for k, v := range s.compressed {
		out[k] = v
	}
	return out
}

// progressivePrune is pruneMessages plus graduated compression of the tool
// results pruning leaves in full. It is what every request goes through: the
// turn's manipulator (turnCompactor.manipulate) calls it in place of
// pruneMessages.
//
// pruneMessages still runs first. Its stubs are for results that are wrong
// (a later edit) or redundant (a later read), and for the size sweep; a
// result it stubbed keeps that stub and gets no level.
//
// Levels are sticky. Each result's level and rendered text are recorded by
// ToolCallID and re-applied on every call, and a level only goes up. New
// levels are assigned in one batch, only when the context pressure (the
// request as sent, against ContextBudget) rises into a higher band than on
// the previous call. The prefix therefore changes at most once per threshold
// crossing, as with the high/low-water sweep. A result stubbed by
// pruneMessages keeps its stub.
//
// msgs is never written through; the result is a new slice.
func (s *Session) progressivePrune(msgs []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	out := s.pruneMessages(msgs)

	// modelMu reads first: modelMu must not nest under prunedMu.
	cfg := s.compactionConfig()
	budget := ContextBudget(cfg, s.contextWindow())
	keep := max(cfg.KeepRecent, 1)

	s.prunedMu.Lock()
	defer s.prunedMu.Unlock()
	if s.pruning.Disabled {
		return out
	}
	if s.compressed == nil {
		s.compressed = map[string]compressedResult{}
	}
	forgetAbsentCompressed(s.compressed, msgs)
	applyCompressed(out, s.compressed, s.prunedIDs)

	band := 0
	if budget > 0 {
		band = pressureBand(float64(estimateTokens(out)) / float64(budget))
	}
	if band > s.compressBand {
		calls := indexToolCalls(msgs)
		protected := trailingToolRun(msgs)
		for i, m := range msgs {
			if m.Role != "tool" || m.ToolCallID == "" || protected[m.ToolCallID] {
				continue
			}
			if _, stubbed := s.prunedIDs[m.ToolCallID]; stubbed {
				continue
			}
			lvl := assignLevel(i, len(msgs), keep, band)
			if prev, ok := s.compressed[m.ToolCallID]; lvl == CompressionFull || (ok && prev.level >= lvl) {
				continue
			}
			info := calls[m.ToolCallID]
			var outlineOf func(string) string
			if pathObservingTools[info.name] {
				outlineOf = s.stubOutline
			}
			text := compressToolOutput(m.Content, info.path, lvl, outlineOf)
			if lvl == CompressionElided {
				// Only here is the tool's name known; see elidedStub.
				name := info.name
				if name == "" {
					name = "tool"
				}
				text = elidedStub(name, info.path, m.Content)
			}
			c := compressedResult{level: lvl, content: text}
			s.compressed[m.ToolCallID] = c
			out[i].Content = c.content
		}
	}
	s.compressBand = band
	return out
}

// forgetAbsentCompressed drops levels whose tool result is no longer in msgs,
// for the same reason as forgetAbsentIDs.
func forgetAbsentCompressed(c map[string]compressedResult, msgs []openai.ChatCompletionMessage) {
	if len(c) == 0 {
		return
	}
	present := make(map[string]bool, len(msgs))
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID != "" {
			present[m.ToolCallID] = true
		}
	}
	for id := range c {
		if !present[id] {
			delete(c, id)
		}
	}
}
