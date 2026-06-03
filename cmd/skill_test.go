package cmd

import "testing"

func TestParseInstallArgsForSkill(t *testing.T) {
	// cmd/skill.go reuses parseInstallArgs (defined in cmd/plugin.go).
	src, ref, yes, err := parseInstallArgs([]string{"--ref", "v2", "--yes", "/local/path"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if src != "/local/path" || ref != "v2" || !yes {
		t.Fatalf("parsed wrong: src=%q ref=%q yes=%v", src, ref, yes)
	}

	if _, _, _, err := parseInstallArgs([]string{}); err == nil {
		t.Fatalf("expected error on missing source")
	}
}

func TestRunSkillCommandUnknownSubcommand(t *testing.T) {
	if code := RunSkillCommand([]string{"frobnicate"}); code != 1 {
		t.Fatalf("expected exit 1 for unknown subcommand, got %d", code)
	}
	if code := RunSkillCommand(nil); code != 1 {
		t.Fatalf("expected exit 1 for no args, got %d", code)
	}
}
