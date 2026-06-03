package cmd

import (
	"fmt"
	"os"

	"github.com/mudler/nib/plugin"
	"github.com/mudler/nib/skill"
)

// RunSkillCommand dispatches `nib skill <sub> ...` and returns an exit code.
func RunSkillCommand(args []string) int {
	if len(args) == 0 {
		skillUsage()
		return 1
	}
	mgr := skill.NewManager(plugin.BaseDir())
	switch args[0] {
	case "install":
		return skillInstall(mgr, args[1:])
	case "list":
		return skillList(mgr)
	case "update":
		return skillByName(args, "update", mgr.Update)
	case "remove":
		return skillByName(args, "remove", mgr.Remove)
	case "enable":
		return skillSetEnabled(mgr, args[1:], true)
	case "disable":
		return skillSetEnabled(mgr, args[1:], false)
	default:
		fmt.Fprintf(os.Stderr, "unknown skill command: %s\n", args[0])
		skillUsage()
		return 1
	}
}

func skillUsage() {
	fmt.Fprintln(os.Stderr, "usage: nib skill <install|list|update|enable|disable|remove> ...")
}

func skillInstall(mgr *skill.Manager, args []string) int {
	// parseInstallArgs is defined in cmd/plugin.go (same package).
	src, ref, yes, err := parseInstallArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "usage: nib skill install [--ref REF] [--yes] <git-url|local-path>")
		return 1
	}

	name, skills, err := mgr.Install(src, ref)
	if err != nil {
		fmt.Fprintf(os.Stderr, "install failed: %v\n", err)
		return 1
	}

	fmt.Printf("Installed skill pack %q — %d skill(s):\n", name, len(skills))
	for _, s := range skills {
		fmt.Printf("  - %s: %s\n", s.Name, s.Description)
	}

	if yes || confirmFn("Enable this skill pack?") {
		if err := mgr.SetEnabled(name, true); err != nil {
			fmt.Fprintf(os.Stderr, "enable failed: %v\n", err)
			return 1
		}
		fmt.Printf("Skill pack %q enabled.\n", name)
		return 0
	}
	fmt.Printf("Skill pack %q installed but left disabled. Enable later: nib skill enable %s\n", name, name)
	return 0
}

func skillList(mgr *skill.Manager) int {
	entries, err := mgr.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "list failed: %v\n", err)
		return 1
	}
	if len(entries) == 0 {
		fmt.Println("No skill packs installed.")
		return 0
	}
	for _, e := range entries {
		status := "disabled"
		if e.Enabled {
			status = "enabled"
		}
		fmt.Printf("%-20s %-9s %s\n", e.Name, status, e.SourceURL)
		skills, err := mgr.Skills(e.Name)
		if err != nil {
			continue
		}
		for _, s := range skills {
			fmt.Printf("    - %s\n", s.Name)
		}
	}
	return 0
}

func skillByName(args []string, verb string, fn func(string) error) int {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: nib skill %s <name>\n", verb)
		return 1
	}
	if err := fn(args[1]); err != nil {
		fmt.Fprintf(os.Stderr, "%s failed: %v\n", verb, err)
		return 1
	}
	fmt.Printf("Skill pack %q %sd.\n", args[1], verb) // "updated" / "removed"
	return 0
}

func skillSetEnabled(mgr *skill.Manager, args []string, enabled bool) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: nib skill enable|disable <name>")
		return 1
	}
	if err := mgr.SetEnabled(args[0], enabled); err != nil {
		fmt.Fprintf(os.Stderr, "failed: %v\n", err)
		return 1
	}
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	fmt.Printf("Skill pack %q %s.\n", args[0], state)
	return 0
}
