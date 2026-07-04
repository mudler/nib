package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/nib/catalog"
	"github.com/mudler/nib/extsource"
	"github.com/mudler/nib/plugin"
	"github.com/mudler/nib/skill"
	"github.com/mudler/nib/types"
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
	case "browse":
		return runBrowse(catalogBaseDir(), catalog.KindSkill)
	case "search":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: nib skill search <query>")
			return 1
		}
		return runSearch(catalogBaseDir(), catalog.KindSkill, strings.Join(args[1:], " "))
	case "source":
		return runSource(catalogBaseDir(), args[1:])
	case "list":
		return skillList(mgr)
	case "update":
		return skillUpdate(mgr, args)
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
	fmt.Fprintln(os.Stderr, "usage: nib skill <install|browse|search|source|list|update|enable|disable|remove> ...")
}

// parseSkillInstallArgs parses `[--ref REF] [--link] [--yes] <git-url|local-path>`
// for `nib skill install`. It is separate from cmd/plugin.go's parseInstallArgs
// so --link stays off `nib plugin install`. --link cannot be combined with --ref.
func parseSkillInstallArgs(args []string) (src, ref string, yes, link bool, err error) {
	fs := flag.NewFlagSet("skill install", flag.ContinueOnError)
	refp := fs.String("ref", "", "git ref (tag or branch) to install")
	yesp := fs.Bool("yes", false, "skip the confirmation prompt")
	linkp := fs.Bool("link", false, "symlink a local dir instead of copying (live edits)")
	if e := fs.Parse(args); e != nil {
		return "", "", false, false, e
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return "", "", false, false, fmt.Errorf("missing <git-url|local-path>")
	}
	src = rest[0]
	if e := fs.Parse(rest[1:]); e != nil {
		return "", "", false, false, e
	}
	if fs.NArg() > 0 {
		return "", "", false, false, fmt.Errorf("unexpected extra arguments: %v", fs.Args())
	}
	if *linkp && *refp != "" {
		return "", "", false, false, fmt.Errorf("--ref cannot be combined with --link")
	}
	return src, *refp, *yesp, *linkp, nil
}

func skillInstall(mgr *skill.Manager, args []string) int {
	src, ref, yes, link, err := parseSkillInstallArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "usage: nib skill install [--ref REF] [--link] [--yes] <git-url|local-path|zip|url|catalog-name>")
		return 1
	}

	// A .zip archive or a bare SKILL.md URL is imported locally; git URLs and
	// local dirs fall through to mgr.Install. --link always uses mgr.Install.
	if !link {
		if name, skills, handled, ierr := skillLocalImport(mgr, src); handled {
			if ierr != nil {
				fmt.Fprintf(os.Stderr, "install failed: %v\n", ierr)
				return 1
			}
			return reportAndMaybeEnable(mgr, name, skills, "Installed", yes)
		}
	}

	// A bare name that is neither a git URL/dir nor a handled local import
	// (zip/SKILL.md URL) is treated as a catalog entry to resolve and install.
	if !link && !looksLikeGitSource(src) {
		return skillCatalogInstall(mgr, catalogBaseDir(), src, yes)
	}

	name, skills, err := mgr.Install(src, ref, link)
	if err != nil {
		fmt.Fprintf(os.Stderr, "install failed: %v\n", err)
		return 1
	}

	how := "Installed"
	if link {
		how = "Linked"
	}
	return reportAndMaybeEnable(mgr, name, skills, how, yes)
}

// looksLikeGitSource reports whether src is a git URL, an scp-style git remote,
// or an existing local directory — i.e. a direct install source rather than a
// bare catalog name. `nib skill install <arg>` treats a non-locator arg as a
// catalog name to resolve.
func looksLikeGitSource(src string) bool {
	if strings.Contains(src, "://") || strings.HasPrefix(src, "git@") {
		return true
	}
	fi, err := os.Stat(src)
	return err == nil && fi.IsDir()
}

// skillCatalogInstall resolves a catalog skill Meta by name, installs it
// (DISABLED, like every install), and runs the shared report/consent tail.
func skillCatalogInstall(mgr *skill.Manager, baseDir, name string, yes bool) int {
	metas, err := mergeCatalog(baseDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "install failed: %v\n", err)
		return 1
	}
	m, err := findCatalogMeta(metas, catalog.KindSkill, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "install failed: %v\n", err)
		return 1
	}
	installed, err := catalog.NewClient().Install(context.Background(), m, baseDir, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "install failed: %v\n", err)
		return 1
	}
	skills, _ := mgr.Skills(installed)
	return reportAndMaybeEnable(mgr, installed, skills, "Installed", yes)
}

// reportAndMaybeEnable prints the install summary, then either enables the pack
// (on --yes or an interactive confirm) or leaves it disabled. It returns the
// process exit code. Imported packs always land disabled; this is the only
// enable path.
func reportAndMaybeEnable(mgr *skill.Manager, name string, skills []types.Skill, how string, yes bool) int {
	fmt.Printf("%s skill pack %q — %d skill(s):\n", how, name, len(skills))
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

// skillLocalImport handles the non-git install sources: a .zip archive and a
// bare SKILL.md URL. It returns handled=false for everything else (git URLs,
// local dirs) so the caller falls through to mgr.Install. The pack name derives
// from the archive/URL basename.
func skillLocalImport(mgr *skill.Manager, src string) (string, []types.Skill, bool, error) {
	switch {
	case strings.HasSuffix(strings.ToLower(src), ".zip"):
		tmp, err := os.MkdirTemp("", "nib-zip-")
		if err != nil {
			return "", nil, true, err
		}
		defer os.RemoveAll(tmp)
		if err := extsource.ExtractZip(src, tmp); err != nil {
			return "", nil, true, err
		}
		name := importName(src, ".zip")
		n, sk, err := mgr.InstallDir(tmp, name, src)
		return n, sk, true, err
	case isSkillMdURL(src):
		tmp, err := os.MkdirTemp("", "nib-url-")
		if err != nil {
			return "", nil, true, err
		}
		defer os.RemoveAll(tmp)
		if err := extsource.FetchSKILLURL(src, tmp); err != nil {
			return "", nil, true, err
		}
		name := importName(strings.TrimSuffix(src, "/SKILL.md"), "")
		n, sk, err := mgr.InstallDir(tmp, name, src)
		return n, sk, true, err
	default:
		return "", nil, false, nil
	}
}

// isSkillMdURL reports whether src is an http(s) URL pointing at a SKILL.md.
func isSkillMdURL(src string) bool {
	return (strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://")) &&
		strings.HasSuffix(src, "/SKILL.md")
}

// importName derives a pack name from a zip/URL basename minus suffix.
func importName(src, suffix string) string {
	base := filepath.Base(strings.TrimSuffix(src, suffix))
	return strings.TrimSuffix(base, ".zip")
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
		line := fmt.Sprintf("%-20s %-9s %s", e.Name, status, e.SourceURL)
		if target, linked := mgr.LinkTarget(e.Name); linked {
			line += fmt.Sprintf("  (linked → %s)", target)
		}
		fmt.Println(line)
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

func skillUpdate(mgr *skill.Manager, args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: nib skill update <name>")
		return 1
	}
	name := args[1]
	if target, linked := mgr.LinkTarget(name); linked {
		fmt.Printf("Skill pack %q is linked (→ %s); edits are already live — nothing to fetch.\n", name, target)
		return 0
	}
	if err := mgr.Update(name); err != nil {
		fmt.Fprintf(os.Stderr, "update failed: %v\n", err)
		return 1
	}
	fmt.Printf("Skill pack %q updated.\n", name)
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
