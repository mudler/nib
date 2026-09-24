# Compaction Artifacts

## Problem

When compaction runs, `fitSummaryInput` truncates long messages to the first 512 bytes and drops whole messages to fit the summarization budget. The summarizer never sees the full content, and the original messages are gone for good. The agent has no way to recover what was lost.

## Solution

Save the full conversation head as an artifact when compaction runs, and surface an `artifact://N` reference in the summary message so the agent can page through the original conversation. Add a search tool so the agent can regex-search artifacts by line (like `grep`) instead of paging linearly. Both features are behind a config flag, and the search tool is only visible to the model when artifacts exist.

## Components

### 1. ArtifactStore.Search (`mcp/artifacts.go`)

Add a `Search` method to `ArtifactStore`:

```go
type SearchResult struct {
    ID   int64  // artifact ID
    Line int    // 1-indexed line number of the match within the artifact
    Text string // rendered snippet: context lines + matching line + context lines
}
```

- Accepts a regex pattern string, compiled case-insensitive via `regexp.Compile`
- Returns `([]SearchResult, error)` — the error is non-nil when the regex is invalid
- Iterates over all artifacts, splits content by newlines, matches line-by-line
- For each match, returns the matching line plus 3 lines of context before and 3 after, rendered with line numbers (same format as `readArtifact`):
  ```
  44| previous context line
  45| previous context line
  46| previous context line
  47> the matching line with the keyword
  48| next context line
  49| next context line
  50| next context line
  ```
- The `>` marker distinguishes the matching line from context lines
- The `Line` field is the line number of the match (not the first context line), so the agent can use it as an offset into `read artifact://N`
- Cap total results at 50 matches
- Return an error if the regex is invalid

### 2. Summary truncation (`chat/compact.go`)

Replace `fitSummaryInput`'s first-512-bytes truncation with the head+tail pattern from `LimitOutput` (`mcp/compress.go`):

- Current: `summaryPieceKeep = 512`, cuts each long piece to the first 512 bytes + `[... N bytes omitted]` marker
- New: keep the first N bytes and the last M bytes, with a truncation notice in between — same approach as `LimitOutput`'s head + tail
- Remove `summaryPieceKeep` and its surrounding dead code (the `cut` function's 512-byte branch and the constant itself)
- `fitSummaryInput`'s signature does not change; only the internal truncation logic does

The constants for head/tail sizes in summary truncation should be smaller than `LimitOutput`'s defaults (the summary input is already a compressed view), but the pattern is the same: head bytes + truncation notice + tail bytes.

### 3. Compaction artifact saving

Both compaction paths save the full head as an artifact:

**End-of-turn (`compactHistory` in `chat/compact.go`):**
- After `splitForCompaction` produces head + tail, but before summarization
- Serialize the full head as a single text blob — same format as `renderMessages` (role + content per message) but without truncation
- Call `s.artifacts.Save("compaction", blob)` — returns an `artifact://N` URI
- Append the URI to the summary message (in `summaryMessage`):
  > "Full conversation before compaction is available at artifact://N — use the read tool with this path to page through it, or search artifacts with the search tool."

**Mid-turn (`turnCompactor.compact` in `chat/midturn.go`):**
- Same logic: after `splitForCompaction`, before `summarize`
- Save the head as an artifact, include the reference in the summary message

**Config guard:**
- Both paths check `CompactionConfig.ArtifactSpill` before saving
- When disabled, compaction proceeds as today — no artifact saved, no reference in the summary

**Error handling:**
- If `s.artifacts` is nil or `Save` fails: log a warning, proceed without the reference
- Compaction must never fail because the artifact system is unavailable

### 4. Search tool (`mcp/filesystem.go`)

New MCP tool registered in the filesystem server:

- **Name:** `search_artifacts`
- **Input:** `pattern` (regex string, case-insensitive)
- **Behavior:** calls `f.artifacts.Search(pattern)`, returns matching snippets with artifact ID and line numbers
- **Output format:** one block per match, showing the artifact URI, context lines with line numbers, and the matching line marked with `>`
- **Visibility:** the tool is only listed to the model when `ArtifactStore.Count() > 0` — checked at tool-list time each turn. When the store is empty (start of session, before any compaction), the tool is hidden.
- **Error handling:**
  - Invalid regex: return a clear error to the model ("invalid regex pattern: ...")
  - No matches: return empty results with a "no matches found" message
  - Empty store: tool is not listed, so the model cannot call it

### 5. Config flag

Add `ArtifactSpill` to `CompactionConfig` in `types`:

```go
type CompactionConfig struct {
    // ... existing fields ...
    ArtifactSpill bool `yaml:"artifact_spill"` // default: true
}
```

- When `true` (default): compaction saves the head as an artifact, summary message includes the `artifact://N` reference
- When `false`: compaction runs as today — no artifact saved, no reference, search tool stays hidden (no artifacts to search)
- Configured via the existing config file, same section as other compaction settings

### 6. Data flow

```
Compaction triggers (end-of-turn or mid-turn)
  → splitForCompaction divides messages into head + tail
  → [if ArtifactSpill enabled] serialize full head as text blob
  → s.artifacts.Save("compaction", blob) → artifact://N
  → fitSummaryInput truncates pieces (head+tail pattern, no more 512-byte cut)
  → summarize produces compressed text
  → summaryMessage includes "artifact://N" reference
  → model reads summary, can:
      → search artifacts with the search tool (regex, line-based results with context)
      → read artifact://N with offset/limit to page through full conversation
```

### 8. Tool naming

The search tool is named `search_artifacts` for clarity — `search` alone could mean too many things. It is registered in the filesystem MCP server alongside `read`, `write`, `edit`, `glob`, and `grep`. When the artifact store is empty, the tool is not listed, so the model never sees it in a fresh session.

### 7. Testing

- **`ArtifactStore.Search`**: unit test with regex patterns including `|` (OR), case-insensitive matching, context lines rendered correctly, 50-match cap, invalid regex error
- **`fitSummaryInput` head+tail**: unit test that truncation produces head + notice + tail (replaces old 512-byte tests), verify `summaryPieceKeep` dead code is removed
- **Compaction saves artifact**: unit test that `compactHistory` saves an artifact and the summary message includes the `artifact://N` reference
- **Mid-turn compaction saves artifact**: unit test that `turnCompactor.compact` saves an artifact and includes the reference
- **Config flag disables saving**: unit test that when `ArtifactSpill` is false, no artifact is saved and the summary message is unchanged
- **Search tool hidden when empty**: unit test that the tool is not listed when `ArtifactStore.Count() == 0`
- **Search tool visible when non-empty**: unit test that the tool is listed when `ArtifactStore.Count() > 0`
- **Error handling**: artifact save failure does not break compaction, invalid regex returns clear error

## Implementation notes

- Work in a separate git worktree
- Open a PR at the end
- Search logic lives on `ArtifactStore` (where the data lives), tool shells it from the filesystem server — approach C from the design discussion
- Summary truncation reuses the head+tail pattern from `LimitOutput` for consistency, but with smaller budget constants appropriate for summary input
- Both compaction paths (end-of-turn and mid-turn) save artifacts for consistency
