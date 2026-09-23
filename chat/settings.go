package chat

import (
	"fmt"

	"github.com/mudler/nib/types"
)

// Live setters for the config keys the TUI's /settings command can apply to a
// running session. Each one touches exactly one piece of state under the lock
// that already guards it, so it is safe from the UI goroutine while a turn
// runs, unlike Reload, which reconnects MCP clients and must run at turn start.

// SetApprovalMode switches tool-call gating to mode ("" / "prompt", "strict",
// "allowlist", "classify" or "auto"), as approval_mode does at startup: "auto"
// turns the approve-everything switch on, and any other mode turns it off, so
// a /yolo toggled on earlier does not outlive an explicit change of mode.
// Grants the user already made (allowedTools, bash prefixes) are kept, as with
// /yolo. "classify" without a configured classifier is refused, and the mode
// stays as it was.
func (s *Session) SetApprovalMode(mode string) error {
	if mode == "classify" && s.approver == nil {
		return fmt.Errorf("classify mode needs a classifier: configure the classifier block")
	}
	s.approvalMu.Lock()
	s.approvalMode = mode
	s.approvalMu.Unlock()
	s.autoApprove.Store(mode == "auto")
	return nil
}

// ApprovalMode is the current approval_mode ("" means prompt). A /yolo
// toggle does not change it; AutoApprove reports that separately.
func (s *Session) ApprovalMode() string {
	if m := s.currentApprovalMode(); m != "" {
		return m
	}
	return "prompt"
}

// HasClassifier reports whether a classifier is configured, so classify
// mode and reply suggestions are available.
func (s *Session) HasClassifier() bool { return s.approver != nil }

// currentApprovalMode reads approvalMode under its lock; decideToolCall runs on
// the turn goroutine while SetApprovalMode runs on the UI's.
func (s *Session) currentApprovalMode() string {
	s.approvalMu.RLock()
	defer s.approvalMu.RUnlock()
	return s.approvalMode
}

// SetCompaction replaces the compaction policy. A zero MaxContextTokens means
// "auto-detect", as it does in the config file, so it keeps the window this
// session already detected instead of zeroing it; a positive one is an
// explicit override and also stops SetModel from re-detecting over it.
func (s *Session) SetCompaction(c types.CompactionConfig) {
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	if c.MaxContextTokens == 0 {
		c.MaxContextTokens = s.compaction.MaxContextTokens
		if c.MaxContextTokens == 0 {
			c.MaxContextTokens = defaultContextTokens
		}
		s.compactionAutoDetected = true
	} else {
		s.compactionAutoDetected = false
	}
	s.compaction = c
}

// compactionConfig returns a copy of the compaction policy, read under the
// lock SetCompaction and SetModel write it under.
func (s *Session) compactionConfig() types.CompactionConfig {
	s.modelMu.RLock()
	defer s.modelMu.RUnlock()
	return s.compaction
}

// SetToolOutputPruning replaces the tool-output pruning policy. Stubs already
// issued stay issued (prunedIDs is untouched): un-stubbing a result would
// change the prompt prefix and cost the cache the stubs were bought for.
func (s *Session) SetToolOutputPruning(p types.ToolOutputPruningConfig) {
	s.prunedMu.Lock()
	s.pruning = p
	s.prunedMu.Unlock()
}
