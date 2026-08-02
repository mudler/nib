package types

import (
	"strings"
	"testing"
)

// The whole point of appending rather than extending config.defaultPrompt: a
// user's `prompt:` REPLACES the default template, so guidance that lived there
// would reach only users who never customized theirs. The reporter in issue #53
// bakes a config with a custom prompt, so a default-only change would miss the
// person who reported the problem.
func TestToolGuidanceSurvivesACustomPrompt(t *testing.T) {
	c := &Config{Prompt: "CUSTOM PROMPT WITH NO GUIDANCE OF ITS OWN"}
	got := c.GetPrompt()

	if !strings.Contains(got, "CUSTOM PROMPT WITH NO GUIDANCE OF ITS OWN") {
		t.Fatalf("custom prompt missing:\n%s", got)
	}
	// Whole phrases, not bare tool names: "read", "edit" and "grep" occur in
	// ordinary prose ("already read", "re-read it"), so asserting on the bare
	// words would pass against text that never names a tool at all.
	for _, want := range []string{
		"read, write and edit for files",
		"glob to find files by name",
		"grep to search file contents",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("guidance does not name the tools (%q missing):\n%s", want, got)
		}
	}
}

// The three behaviors the guidance exists to change, each pinned by a phrase
// specific enough that deleting the paragraph fails the test.
func TestToolGuidanceCoversTheReportedFailureModes(t *testing.T) {
	c := &Config{Prompt: "BASE"}
	got := c.GetPrompt()

	cases := []struct {
		phrase string
		why    string
	}{
		{"whole file by default", "traces showed the model reading slices via sed instead of whole files"},
		{"Do not re-read a file", "traces showed repeated re-reads of files already in context"},
		{"out of date", "a read is stale once the file has been edited"},
		{"Do not use cat, sed, head, tail or shell grep for these", "the literal symptom in issue #53: traces showed `sed -n '95,115p'` where a read call belonged"},
	}
	for _, tc := range cases {
		if !strings.Contains(got, tc.phrase) {
			t.Fatalf("guidance is missing %q (%s):\n%s", tc.phrase, tc.why, got)
		}
	}
}

// Ordering is load-bearing: after the skills index so it does not separate the
// index from the prompt that introduces it, and BEFORE prompt_fragments so a
// user's own fragment can still countermand the guidance.
func TestToolGuidanceOrderedAfterSkillsBeforeFragments(t *testing.T) {
	c := &Config{
		Prompt:          "BASE",
		Skills:          []Skill{{Name: "deploy", Description: "ship to prod"}},
		PromptFragments: []string{"FRAGMENT MARKER"},
	}
	got := c.GetPrompt()

	skills := strings.Index(got, "load_skill")
	guidance := strings.Index(got, "whole file by default")
	fragment := strings.Index(got, "FRAGMENT MARKER")

	if skills < 0 || guidance < 0 || fragment < 0 {
		t.Fatalf("missing section: skills=%d guidance=%d fragment=%d\n%s", skills, guidance, fragment, got)
	}
	if !(skills < guidance && guidance < fragment) {
		t.Fatalf("wrong order: skills=%d guidance=%d fragment=%d\n%s", skills, guidance, fragment, got)
	}
}

// The act-don't-narrate instruction used to live in config.defaultPrompt, which
// a custom `prompt:` replaces wholesale. Issue #53 reported the model announcing
// actions instead of taking them, from a deployment with a custom prompt — i.e.
// one that never received this line.
func TestActDontNarrateSurvivesACustomPrompt(t *testing.T) {
	c := &Config{Prompt: "CUSTOM PROMPT"}
	got := c.GetPrompt()

	if !strings.Contains(got, "Always act by CALLING the available tools") {
		t.Fatalf("act-don't-narrate instruction missing from a custom-prompt session:\n%s", got)
	}
	if !strings.Contains(got, "describing an action does not perform it") {
		t.Fatalf("the consequence clause is what makes the instruction land; it is missing:\n%s", got)
	}
}

// A config with no skills still gets the guidance — the two are independent.
func TestToolGuidanceAppearsWithoutSkills(t *testing.T) {
	c := &Config{Prompt: "BASE"}
	if !strings.Contains(c.GetPrompt(), "whole file by default") {
		t.Fatalf("guidance should not depend on skills being configured:\n%s", c.GetPrompt())
	}
}

// editFile (mcp/filesystem.go) REFUSES when the old string occurs more than
// once — "old string appears %d times in file, use all=true" — it never
// replaces the first of several. Guidance promising first-match semantics makes
// the model send a bare identifier as `old` and burn a turn on the error.
func TestToolGuidanceStatesTheRealEditContract(t *testing.T) {
	got := (&Config{Prompt: "BASE"}).GetPrompt()

	for _, want := range []string{"appears exactly once", "all=true"} {
		if !strings.Contains(got, want) {
			t.Fatalf("edit guidance missing %q; the tool requires a unique old string:\n%s", want, got)
		}
	}
	// The old wording, and any revival of it, claims a contract the tool does
	// not implement.
	if strings.Contains(got, "replaces one exact occurrence") {
		t.Fatalf("guidance claims first-occurrence replacement; editFile errors out instead:\n%s", got)
	}
	// write marks the file seen too (fileSystem.write), so demanding a read
	// after a write would cost a call the tool never asked for.
	if !strings.Contains(got, "read or written the file first") {
		t.Fatalf("a written file is editable without re-reading it:\n%s", got)
	}
}

// readFile applies no size cap: limit=0 returns every line. A model told to
// never request ranges will read a 20k-line lockfile whole and blow the context
// window of exactly the small local models this text is written for.
func TestToolGuidanceLeavesAnEscapeHatchForHugeFiles(t *testing.T) {
	got := (&Config{Prompt: "BASE"}).GetPrompt()

	if !strings.Contains(got, "whole file by default") {
		t.Fatalf("whole-file reads must stay the default:\n%s", got)
	}
	if !strings.Contains(got, "offset and limit") {
		t.Fatalf("guidance must name offset and limit as the way out for oversized files:\n%s", got)
	}
}

// grepFiles caps results at maxMatches = 50 and reports Count as the number
// RETURNED, so truncation is invisible: a model that greps a common symbol sees
// 50 hits and concludes it has every call site.
func TestToolGuidanceWarnsGrepTruncates(t *testing.T) {
	got := (&Config{Prompt: "BASE"}).GetPrompt()

	if !strings.Contains(got, "at most 50 matches") {
		t.Fatalf("guidance must state grep's 50-match cap:\n%s", got)
	}
	if !strings.Contains(got, "may be incomplete") {
		t.Fatalf("the cap only matters if the model knows a full result may be truncated:\n%s", got)
	}
}

// globFiles filters directories out of its results, so no dedicated tool lists
// a directory or reveals subdirectories. Banning ls and find would leave the
// model with no sanctioned way to see what a tree contains.
func TestToolGuidanceDoesNotBanDirectoryListing(t *testing.T) {
	got := (&Config{Prompt: "BASE"}).GetPrompt()

	start := strings.Index(got, "Do not use ")
	if start < 0 {
		t.Fatalf("the shell-ban sentence is gone entirely:\n%s", got)
	}
	ban := got[start:]
	ban = ban[:strings.Index(ban, "\n")]
	for _, cmd := range []string{"ls", "find"} {
		if strings.Contains(ban, " "+cmd+",") || strings.Contains(ban, " "+cmd+" ") {
			t.Fatalf("%q is banned but glob never lists directories, so nothing replaces it: %q", cmd, ban)
		}
	}
}
