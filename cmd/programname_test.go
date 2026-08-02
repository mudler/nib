package cmd

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureOutput redirects BOTH os.Stdout and os.Stderr for the duration of fn
// and returns them separately. The management subcommands write to whichever
// suits the message, so a test that only watched one would keep missing half
// the strings this file is about.
func captureOutput(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout, os.Stderr = outW, errW

	collect := func(r *os.File) <-chan string {
		done := make(chan string, 1)
		go func() {
			var sb strings.Builder
			_, _ = io.Copy(&sb, bufio.NewReader(r))
			done <- sb.String()
		}()
		return done
	}
	outDone, errDone := collect(outR), collect(errR)

	fn()

	_ = outW.Close()
	_ = errW.Close()
	os.Stdout, os.Stderr = origOut, origErr
	return <-outDone, <-errDone
}

// manageInvocations are the management calls that print something a user is
// told to RUN. Each is exercised twice, once standalone and once embedded, so
// one list drives both the "the name reaches cmd" test and the "standalone is
// byte-identical" test below.
//
// Every entry is chosen to reach a distinct printing site: the three
// dispatchers' bare usage, their unknown-subcommand path, the per-verb usage
// lines, and the shared source handler both plugin and skill route into.
var manageInvocations = []struct {
	name string
	run  func(programName, baseDir string) int
}{
	{"plugin bare", func(p, b string) int { return RunPluginCommand(p, b, nil) }},
	{"plugin unknown", func(p, b string) int { return RunPluginCommand(p, b, []string{"frobnicate"}) }},
	{"plugin search", func(p, b string) int { return RunPluginCommand(p, b, []string{"search"}) }},
	{"plugin install", func(p, b string) int { return RunPluginCommand(p, b, []string{"install"}) }},
	{"plugin update", func(p, b string) int { return RunPluginCommand(p, b, []string{"update"}) }},
	{"plugin remove", func(p, b string) int { return RunPluginCommand(p, b, []string{"remove"}) }},
	{"plugin enable", func(p, b string) int { return RunPluginCommand(p, b, []string{"enable"}) }},
	{"plugin source", func(p, b string) int { return RunPluginCommand(p, b, []string{"source"}) }},
	{"plugin source add", func(p, b string) int { return RunPluginCommand(p, b, []string{"source", "add"}) }},
	{"plugin source remove", func(p, b string) int { return RunPluginCommand(p, b, []string{"source", "remove"}) }},
	{"skill bare", func(p, b string) int { return RunSkillCommand(p, b, nil) }},
	{"skill unknown", func(p, b string) int { return RunSkillCommand(p, b, []string{"frobnicate"}) }},
	{"skill search", func(p, b string) int { return RunSkillCommand(p, b, []string{"search"}) }},
	{"skill install", func(p, b string) int { return RunSkillCommand(p, b, []string{"install"}) }},
	{"skill update", func(p, b string) int { return RunSkillCommand(p, b, []string{"update"}) }},
	{"skill remove", func(p, b string) int { return RunSkillCommand(p, b, []string{"remove"}) }},
	{"skill enable", func(p, b string) int { return RunSkillCommand(p, b, []string{"enable"}) }},
	{"skill source", func(p, b string) int { return RunSkillCommand(p, b, []string{"source"}) }},
	{"mcp bare", func(p, b string) int { return RunMCPCommand(p, b, nil) }},
	{"mcp unknown", func(p, b string) int { return RunMCPCommand(p, b, []string{"frobnicate"}) }},
	{"mcp add", func(p, b string) int { return RunMCPCommand(p, b, []string{"add"}) }},
	{"mcp remove", func(p, b string) int { return RunMCPCommand(p, b, []string{"remove"}) }},
	{"mcp test", func(p, b string) int { return RunMCPCommand(p, b, []string{"test"}) }},
	{"mcp enable", func(p, b string) int { return RunMCPCommand(p, b, []string{"enable"}) }},
}

// A usage line is an instruction: it tells the reader what to type. Embedded as
// `local-ai chat`, nib telling them to type `nib plugin install ...` names a
// binary they do not have.
func TestManagementUsageStringsUseTheProgramName(t *testing.T) {
	for _, c := range manageInvocations {
		t.Run(c.name, func(t *testing.T) {
			base := t.TempDir()
			stdout, stderr := captureOutput(t, func() { c.run("local-ai chat", base) })
			all := stdout + stderr
			if !strings.Contains(all, "local-ai chat ") {
				t.Fatalf("output never names the program: %q", all)
			}
			if strings.Contains(all, "nib ") {
				t.Fatalf("output still tells the user to run nib: %q", all)
			}
		})
	}
}

// The reported line. A plugin install that cannot get consent leaves the plugin
// disabled and prints how to enable it later, and that hint is the one an
// embedded user is most likely to act on, because a non-interactive install
// reaches it every time without --yes.
func TestPluginDisabledHintUsesTheProgramName(t *testing.T) {
	base := t.TempDir()
	src := writePluginZip(t, "demo-plug")
	declineConsent(t)

	stdout, _ := captureOutput(t, func() {
		if code := RunPluginCommand("local-ai chat", base, []string{"install", src}); code != 0 {
			t.Errorf("install exit = %d", code)
		}
	})
	if !strings.Contains(stdout, "local-ai chat plugin enable demo-plug") {
		t.Fatalf("the enable hint does not name the embedding program: %q", stdout)
	}
	if strings.Contains(stdout, "nib plugin enable") {
		t.Fatalf("the enable hint still points at a binary the user does not have: %q", stdout)
	}
}

// The skill half of the same hint, which fires on the same non-interactive
// path.
func TestSkillDisabledHintUsesTheProgramName(t *testing.T) {
	base := t.TempDir()
	src := writeSkillDir(t, "demo-skill")
	declineConsent(t)

	stdout, _ := captureOutput(t, func() {
		if code := RunSkillCommand("local-ai chat", base, []string{"install", src}); code != 0 {
			t.Errorf("install exit = %d", code)
		}
	})
	if !strings.Contains(stdout, "local-ai chat skill enable demo-skill") {
		t.Fatalf("the enable hint does not name the embedding program: %q", stdout)
	}
	if strings.Contains(stdout, "nib skill enable") {
		t.Fatalf("the enable hint still points at a binary the user does not have: %q", stdout)
	}
}

// `mcp add` ends with a command to verify the server with. The sentence also
// says "the next nib session", which is prose naming the tool rather than an
// instruction, and is deliberately left alone.
func TestMCPAddHintUsesTheProgramName(t *testing.T) {
	base := t.TempDir()
	stdout, _ := captureOutput(t, func() {
		if code := RunMCPCommand("local-ai chat", base, []string{"add", "demo", "--", "demo-mcp"}); code != 0 {
			t.Errorf("add exit = %d", code)
		}
	})
	if !strings.Contains(stdout, "local-ai chat mcp test demo") {
		t.Fatalf("the verify hint does not name the embedding program: %q", stdout)
	}
	if strings.Contains(stdout, "nib mcp test") {
		t.Fatalf("the verify hint still points at a binary the user does not have: %q", stdout)
	}
}

// Standalone nib must be byte-identical, which is the same guarantee
// TestInitScriptDefaultsToNib makes for the shell integration: an empty program
// name is not a third rendering, it is exactly "nib".
func TestManagementStringsDefaultToNib(t *testing.T) {
	for _, c := range manageInvocations {
		t.Run(c.name, func(t *testing.T) {
			emptyOut, emptyErr := captureOutput(t, func() { c.run("", t.TempDir()) })
			nibOut, nibErr := captureOutput(t, func() { c.run("nib", t.TempDir()) })
			if emptyOut != nibOut || emptyErr != nibErr {
				t.Fatalf("an empty program name differs from the explicit \"nib\":\n empty = %q / %q\n   nib = %q / %q",
					emptyOut, emptyErr, nibOut, nibErr)
			}
			if !strings.Contains(emptyOut+emptyErr, "nib ") {
				t.Fatalf("standalone output stopped naming nib: %q", emptyOut+emptyErr)
			}
		})
	}
}

// A program name is printed, not executed, but it is still embedder-supplied
// text going into a line the user reads as a command. Newlines would let it
// forge extra output lines, so the words are normalized to single spaces.
func TestProgramNameIsNormalizedToOneLine(t *testing.T) {
	_, stderr := captureOutput(t, func() {
		RunPluginCommand("evil\nusage: rm -rf /", t.TempDir(), nil)
	})
	if strings.Count(strings.TrimSpace(stderr), "\n") != 0 {
		t.Fatalf("a program name with a newline forged an extra output line: %q", stderr)
	}
}

// declineConsent makes the install confirmation answer "no" for this test, which
// is what a non-interactive embedded install does when it hits EOF on stdin.
func declineConsent(t *testing.T) {
	t.Helper()
	orig := confirmFn
	confirmFn = func(string) bool { return false }
	t.Cleanup(func() { confirmFn = orig })
}

// writePluginZip builds the smallest installable plugin archive: a manifest
// with a name.
func writePluginZip(t *testing.T, name string) string {
	t.Helper()
	zp := filepath.Join(t.TempDir(), name+".zip")
	writeTestZip(t, zp, map[string]string{
		"nib-plugin.yaml": "name: " + name + "\ndescription: test plugin\n",
	})
	return zp
}

// writeSkillDir builds the smallest installable skill pack: a directory holding
// one SKILL.md.
func writeSkillDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: test skill\n---\n\nDo the thing.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// skill.Manager builds its own suggestions, so the name has to reach the skill
// package too. A second install of the same pack is the common way to see it.
func TestSkillDuplicateHintUsesTheProgramName(t *testing.T) {
	base := t.TempDir()
	src := writeSkillDir(t, "dupe-skill")
	declineConsent(t)

	captureOutput(t, func() { RunSkillCommand("local-ai chat", base, []string{"install", src}) })
	_, stderr := captureOutput(t, func() {
		if code := RunSkillCommand("local-ai chat", base, []string{"install", src}); code == 0 {
			t.Error("a duplicate install should fail")
		}
	})
	if !strings.Contains(stderr, "`local-ai chat skill update dupe-skill`") ||
		!strings.Contains(stderr, "`local-ai chat skill remove dupe-skill`") {
		t.Fatalf("the duplicate-pack suggestion does not name the embedding program: %q", stderr)
	}
	if strings.Contains(stderr, "nib skill") {
		t.Fatalf("the suggestion still points at a binary the user does not have: %q", stderr)
	}
}

// The catalog install path builds its OWN skill.Manager inside catalog.Client,
// so it is a second place the name has to reach. Missing it would leave exactly
// this branch printing "nib" while the direct branch printed the right name.
func TestSkillCatalogDuplicateHintUsesTheProgramName(t *testing.T) {
	root, _ := twoCatalogs(t)
	declineConsent(t)

	captureOutput(t, func() { RunSkillCommand("local-ai chat", root, []string{"install", "--yes", "injected-skill"}) })
	_, stderr := captureOutput(t, func() {
		if code := RunSkillCommand("local-ai chat", root, []string{"install", "--yes", "injected-skill"}); code == 0 {
			t.Error("a duplicate catalog install should fail")
		}
	})
	if !strings.Contains(stderr, "`local-ai chat skill update injected-skill`") {
		t.Fatalf("the catalog path's suggestion does not name the embedding program: %q", stderr)
	}
	if strings.Contains(stderr, "nib skill") {
		t.Fatalf("the catalog path still points at nib: %q", stderr)
	}
}

// The other suggestion skill.Manager makes: a source with no SKILL.md is
// probably a plugin, and it says so by naming a command.
func TestSkillWrongKindHintUsesTheProgramName(t *testing.T) {
	base := t.TempDir()
	empty := t.TempDir() // a directory with no SKILL.md anywhere in it

	_, stderr := captureOutput(t, func() {
		if code := RunSkillCommand("local-ai chat", base, []string{"install", empty}); code == 0 {
			t.Error("installing a pack with no SKILL.md should fail")
		}
	})
	if !strings.Contains(stderr, "`local-ai chat plugin install`") {
		t.Fatalf("the wrong-kind suggestion does not name the embedding program: %q", stderr)
	}
	if strings.Contains(stderr, "nib plugin install") {
		t.Fatalf("the wrong-kind suggestion still points at nib: %q", stderr)
	}
}

// Standalone control for all three skill suggestions: an empty program name
// must render exactly what it rendered before, which is "nib".
func TestSkillHintsDefaultToNib(t *testing.T) {
	base := t.TempDir()
	src := writeSkillDir(t, "dupe-skill")
	empty := t.TempDir()
	declineConsent(t)

	captureOutput(t, func() { RunSkillCommand("", base, []string{"install", src}) })
	_, dupe := captureOutput(t, func() { RunSkillCommand("", base, []string{"install", src}) })
	if !strings.Contains(dupe, "`nib skill update dupe-skill`") ||
		!strings.Contains(dupe, "`nib skill remove dupe-skill`") {
		t.Fatalf("standalone duplicate suggestion changed: %q", dupe)
	}
	_, kind := captureOutput(t, func() { RunSkillCommand("", base, []string{"install", empty}) })
	if !strings.Contains(kind, "`nib plugin install`") {
		t.Fatalf("standalone wrong-kind suggestion changed: %q", kind)
	}
}
