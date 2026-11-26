package plugin

import "testing"

func TestParseSkillMarkdown(t *testing.T) {
	data := []byte("---\nname: demo\ndescription: a demo skill\nallowed-tools: Bash, Read\n---\nbody line one\nbody line two\n")
	name, desc, tools, body := ParseSkillMarkdown(data)
	if name != "demo" {
		t.Fatalf("name = %q", name)
	}
	if desc != "a demo skill" {
		t.Fatalf("desc = %q", desc)
	}
	if len(tools) == 0 {
		t.Fatalf("expected aliased tools, got none")
	}
	if body != "body line one\nbody line two\n" {
		t.Fatalf("body = %q", body)
	}
}
