package cmd

import "testing"

func TestLooksLikeGitSource(t *testing.T) {
	yes := []string{"https://github.com/o/r", "git@github.com:o/r.git", "http://x/y"}
	for _, s := range yes {
		if !looksLikeGitSource(s) {
			t.Errorf("%q should look like a git source", s)
		}
	}
	no := []string{"greeter", "some-catalog-name"}
	for _, s := range no {
		if looksLikeGitSource(s) {
			t.Errorf("%q should NOT look like a git source", s)
		}
	}
	// An existing local directory is a git/dir source, not a catalog name.
	dir := t.TempDir()
	if !looksLikeGitSource(dir) {
		t.Errorf("existing dir %q should look like a local source", dir)
	}
}

func TestRunSkillCommand_SourceList(t *testing.T) {
	// `skill source list` must not error out of the box (bundled always present).
	// Runs against the real BaseDir but only reads config; exit 0 is the contract.
	if code := runSource(t.TempDir(), []string{"list"}); code != 0 {
		t.Fatalf("source list exit=%d", code)
	}
}
