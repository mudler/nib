package cmd

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/plugin"
	"github.com/mudler/nib/skill"
)

// isolatedHome points nib's default resolution at an empty throwaway root, so
// any state a command creates there is a leak out of the injected root. It
// returns that default root (which must stay untouched).
func isolatedHome(t *testing.T) string {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	// plugin.BaseDir falls back to the legacy wiz dir only when the nib dir is
	// absent, so create the nib dir to pin the default to a single known path.
	root := filepath.Join(xdg, "nib")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// assertUntouched fails if the command wrote any registry or config file into
// nib's default root.
func assertUntouched(t *testing.T, defaultRoot string) {
	t.Helper()
	for _, name := range []string{"plugins.yaml", "skills.yaml", "sources.yaml", "config.yaml"} {
		if _, err := os.Stat(filepath.Join(defaultRoot, name)); !os.IsNotExist(err) {
			t.Fatalf("%s written into the default root %s (err = %v)", name, defaultRoot, err)
		}
	}
}

// seedPlugin installs a disabled plugin under root without going through git.
func seedPlugin(t *testing.T, root, name string) {
	t.Helper()
	pdir := filepath.Join(plugin.PluginsDir(root), name)
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "nib-plugin.yaml"), []byte("name: "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plugins.yaml"),
		[]byte("plugins:\n  - name: "+name+"\n    enabled: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedSkillPack installs a disabled skill pack under root.
func seedSkillPack(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(skill.SkillsDir(root), name, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: demo\ndescription: d\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := skill.LoadRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	reg.Upsert(skill.Entry{Name: name, Enabled: false})
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
}

// `nib plugin enable` under an override must read and write the injected
// registry. The plugin exists only there, so resolving the default root would
// fail the enable outright, and the flipped flag must persist in the injected
// plugins.yaml rather than a fresh one under ~/.config/nib.
func TestRunPluginCommandUsesInjectedBaseDir(t *testing.T) {
	defaultRoot := isolatedHome(t)
	root := t.TempDir()
	seedPlugin(t, root, "demo")

	if code := RunPluginCommand(root, []string{"enable", "demo"}); code != 0 {
		t.Fatalf("plugin enable exit = %d, want 0", code)
	}
	reg, err := plugin.LoadRegistry(root)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	e := reg.Find("demo")
	if e == nil || !e.Enabled {
		t.Fatalf("injected registry not updated: %+v", e)
	}
	assertUntouched(t, defaultRoot)
}

// The catalog subcommands took the root from catalogBaseDir(), which always
// resolved the default. `source add` must persist into the injected root.
func TestRunPluginCommandSourceUsesInjectedBaseDir(t *testing.T) {
	defaultRoot := isolatedHome(t)
	root := t.TempDir()

	if code := RunPluginCommand(root, []string{"source", "add", "https://example.invalid/skills.yaml"}); code != 0 {
		t.Fatalf("plugin source add exit = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(root, "sources.yaml")); err != nil {
		t.Fatalf("sources.yaml not written into the injected root: %v", err)
	}
	assertUntouched(t, defaultRoot)
}

// `nib skill enable` under an override must read and write the injected registry.
func TestRunSkillCommandUsesInjectedBaseDir(t *testing.T) {
	defaultRoot := isolatedHome(t)
	root := t.TempDir()
	seedSkillPack(t, root, "pack")

	if code := RunSkillCommand(root, []string{"enable", "pack"}); code != 0 {
		t.Fatalf("skill enable exit = %d, want 0", code)
	}
	reg, err := skill.LoadRegistry(root)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	e := reg.Find("pack")
	if e == nil || !e.Enabled {
		t.Fatalf("injected registry not updated: %+v", e)
	}
	assertUntouched(t, defaultRoot)
}

// `nib mcp add` writes to a config file. Under an override that file is
// <root>/config.yaml; the user's real config must not be created or edited.
func TestRunMCPCommandUsesInjectedBaseDir(t *testing.T) {
	defaultRoot := isolatedHome(t)
	root := t.TempDir()

	if code := RunMCPCommand(root, []string{"add", "demo", "--", "demo-mcp"}); code != 0 {
		t.Fatalf("mcp add exit = %d, want 0", code)
	}
	data, err := os.ReadFile(filepath.Join(root, "config.yaml"))
	if err != nil {
		t.Fatalf("config.yaml not written into the injected root: %v", err)
	}
	if !strings.Contains(string(data), "demo-mcp") {
		t.Fatalf("injected config missing the server: %q", data)
	}
	assertUntouched(t, defaultRoot)
}

// --- catalog read-side coverage -------------------------------------------
//
// The bare-catalog-name install branch and the browse/search subcommands read
// <root>/sources.yaml and then fetch every enabled source. These helpers stand
// up a local index (and a downloadable zip) per root, so the whole path runs
// offline against 127.0.0.1 and each root sees a DIFFERENT catalog. A root that
// resolves to the wrong place therefore sees the wrong entry, which is what
// makes the assertions discriminating rather than merely green.

// catalogServer serves an index.json advertising one plugin and one skill, both
// named "<prefix>-plug" / "<prefix>-skill", installable from a zip it also
// serves. It returns the index URL.
func catalogServer(t *testing.T, prefix string) string {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	mux.HandleFunc("/plug.zip", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(zipBytes(t, map[string]string{"nib-plugin.yaml": "name: " + prefix + "-plug\ndescription: d\n"}))
	})
	mux.HandleFunc("/skill.zip", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(zipBytes(t, map[string]string{"demo/SKILL.md": "---\nname: " + prefix + "-demo\ndescription: d\n---\nbody\n"}))
	})
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"extensions":[
			{"kind":"plugin","name":%q,"description":"from the %s catalog","category":"test","zipURL":%q},
			{"kind":"skill","name":%q,"description":"from the %s catalog","category":"test","zipURL":%q}
		]}`, prefix+"-plug", prefix, srv.URL+"/plug.zip",
			prefix+"-skill", prefix, srv.URL+"/skill.zip")
	})
	return srv.URL + "/index.json"
}

// zipBytes builds a zip archive in memory.
func zipBytes(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range entries {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// writeSources points root at indexURL and disables the two networked default
// sources, so a merge touches nothing outside 127.0.0.1. The bundled source is
// non-overridable but reads an index compiled into the binary.
func writeSources(t *testing.T, root, indexURL string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "sources:\n" +
		"  - label: agentskills.io\n    url: https://agentskills.io\n    kind: wellknown\n    trust: official\n    enabled: false\n" +
		"  - label: openclaw/agent-skills\n    url: https://github.com/openclaw/agent-skills\n    kind: github\n    trust: community\n    enabled: false\n" +
		"  - label: local\n    url: " + indexURL + "\n    kind: index\n    trust: community\n    enabled: true\n"
	if err := os.WriteFile(filepath.Join(root, "sources.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// twoCatalogs gives the injected root and nib's default root a catalog each,
// then returns the injected root. Reading the wrong root yields the wrong
// entries, so an assertion on entry names is a real discriminator.
func twoCatalogs(t *testing.T) (root, defaultRoot string) {
	t.Helper()
	defaultRoot = isolatedHome(t)
	writeSources(t, defaultRoot, catalogServer(t, "default"))
	root = t.TempDir()
	writeSources(t, root, catalogServer(t, "injected"))
	return root, defaultRoot
}

// `nib plugin search` merges the catalog from <root>/sources.yaml. Under an
// override that is the injected root's catalog, never the user's.
func TestRunPluginCommandSearchReadsInjectedCatalog(t *testing.T) {
	root, _ := twoCatalogs(t)
	out := captureStdout(t, func() {
		if code := RunPluginCommand(root, []string{"search", "catalog"}); code != 0 {
			t.Errorf("plugin search exit = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "injected-plug") {
		t.Fatalf("search did not read the injected catalog: %q", out)
	}
	if strings.Contains(out, "default-plug") {
		t.Fatalf("search read the user's real catalog: %q", out)
	}
}

// Same for browse, which takes the other runBrowse/runSearch branch.
func TestRunSkillCommandBrowseReadsInjectedCatalog(t *testing.T) {
	root, _ := twoCatalogs(t)
	out := captureStdout(t, func() {
		if code := RunSkillCommand(root, []string{"browse"}); code != 0 {
			t.Errorf("skill browse exit = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "injected-skill") {
		t.Fatalf("browse did not read the injected catalog: %q", out)
	}
	if strings.Contains(out, "default-skill") {
		t.Fatalf("browse read the user's real catalog: %q", out)
	}
}

// `nib plugin install <bare-name>` resolves the name through mergeCatalog,
// which reads <root>/sources.yaml. This is the branch the root parameter was
// added for: the entry exists ONLY in the injected catalog, so resolving the
// default root fails the install outright.
func TestRunPluginCommandCatalogInstallUsesInjectedRoot(t *testing.T) {
	root, defaultRoot := twoCatalogs(t)
	captureStdout(t, func() {
		if code := RunPluginCommand(root, []string{"install", "--yes", "injected-plug"}); code != 0 {
			t.Errorf("plugin install exit = %d, want 0", code)
		}
	})
	reg, err := plugin.LoadRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	if e := reg.Find("injected-plug"); e == nil || !e.Enabled {
		t.Fatalf("catalog plugin not installed into the injected root: %+v", e)
	}
	if _, err := os.Stat(filepath.Join(defaultRoot, "plugins.yaml")); !os.IsNotExist(err) {
		t.Fatalf("wrote a registry into the user's real nib root: err = %v", err)
	}
}

// Same for the skill dispatcher's bare-catalog-name branch.
func TestRunSkillCommandCatalogInstallUsesInjectedRoot(t *testing.T) {
	root, defaultRoot := twoCatalogs(t)
	captureStdout(t, func() {
		if code := RunSkillCommand(root, []string{"install", "--yes", "injected-skill"}); code != 0 {
			t.Errorf("skill install exit = %d, want 0", code)
		}
	})
	reg, err := skill.LoadRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	if e := reg.Find("injected-skill"); e == nil || !e.Enabled {
		t.Fatalf("catalog skill pack not installed into the injected root: %+v", e)
	}
	if _, err := os.Stat(filepath.Join(defaultRoot, "skills.yaml")); !os.IsNotExist(err) {
		t.Fatalf("wrote a registry into the user's real nib root: err = %v", err)
	}
}
