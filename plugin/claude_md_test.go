package plugin

import "testing"

func TestSplitFrontmatter(t *testing.T) {
	fm, body := splitFrontmatter([]byte("---\nname: foo\ndescription: bar\n---\nhello body\nmore\n"))
	if string(fm) != "name: foo\ndescription: bar\n" {
		t.Fatalf("frontmatter wrong: %q", fm)
	}
	if body != "hello body\nmore\n" {
		t.Fatalf("body wrong: %q", body)
	}
	fm, body = splitFrontmatter([]byte("just body\n"))
	if len(fm) != 0 || body != "just body\n" {
		t.Fatalf("no-frontmatter wrong: fm=%q body=%q", fm, body)
	}
}
