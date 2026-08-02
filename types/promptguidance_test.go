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
		{"Do not use cat, sed, head, tail, ls, find or shell grep for these", "the literal symptom in issue #53: traces showed `sed -n '95,115p'` where a read call belonged"},
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
