package mcp

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

func TestResolveCUAConfigUsesTopLevelFieldsThenLegacyFallbacks(t *testing.T) {
	t.Setenv("NIB_CUA_DRIVER_CMD", "/env/cua-driver")
	topEnv := map[string]string{"SOURCE": "top-level"}
	legacyArgs := []string{"mcp", "--legacy-args"}
	cfg := types.Config{
		CUA: types.CUAConfig{
			Command: "/top/cua-driver",
			Env:     topEnv,
		},
		Computer: types.ComputerConfig{
			Command:   "/legacy/cua-driver",
			Args:      legacyArgs,
			Env:       map[string]string{"SOURCE": "legacy"},
			SessionID: "legacy-session",
		},
	}

	got := resolveCUAConfig(cfg)
	want := types.CUAConfig{
		Command:   "/top/cua-driver",
		Args:      []string{"mcp", "--legacy-args"},
		Env:       map[string]string{"SOURCE": "top-level"},
		SessionID: "legacy-session",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveCUAConfig() = %+v, want %+v", got, want)
	}

	got.Args[0] = "mutated"
	got.Env["SOURCE"] = "mutated"
	if legacyArgs[0] != "mcp" {
		t.Fatalf("resolved Args alias legacy config: %v", legacyArgs)
	}
	if topEnv["SOURCE"] != "top-level" {
		t.Fatalf("resolved Env aliases top-level config: %v", topEnv)
	}

	fromEnv := resolveCUAConfig(types.Config{})
	if fromEnv.Command != "/env/cua-driver" || !reflect.DeepEqual(fromEnv.Args, []string{"mcp"}) {
		t.Fatalf("empty config resolved to %+v, want env command and default mcp args", fromEnv)
	}
	if fromEnv.SessionID != "" {
		t.Fatalf("resolver minted SessionID %q; runtime owns session minting", fromEnv.SessionID)
	}

	t.Setenv("NIB_CUA_DRIVER_CMD", "")
	if got := resolveCUAConfig(types.Config{}).Command; got != "cua-driver" {
		t.Fatalf("command without config or env = %q, want cua-driver", got)
	}
}

func TestBrowserBackendDefaultsToChromedp(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", want: "chromedp"},
		{name: "whitespace", in: " \t ", want: "chromedp"},
		{name: "chromedp canonicalized", in: " ChromeDP ", want: "chromedp"},
		{name: "cua canonicalized", in: " CUA ", want: "cua"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := browserBackend(types.BrowserConfig{Backend: tt.in})
			if err != nil {
				t.Fatalf("browserBackend(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("browserBackend(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestBrowserBackendRejectsUnknownValue(t *testing.T) {
	_, err := browserBackend(types.BrowserConfig{Backend: "selenium"})
	if err == nil {
		t.Fatal("browserBackend(selenium) succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "selenium") || !strings.Contains(err.Error(), "chromedp") ||
		!strings.Contains(err.Error(), "cua") {
		t.Fatalf("error %q should name the invalid value and accepted backends", err)
	}
}

func TestCuaBrowserRejectsProfileDir(t *testing.T) {
	err := validateBrowserConfig(types.BrowserConfig{Backend: "cua", ProfileDir: "/tmp/chrome-profile"})
	if err == nil {
		t.Fatal("Cua backend accepted browser.profile_dir")
	}
	if !strings.Contains(err.Error(), "profile_dir") || !strings.Contains(err.Error(), "profile_name") {
		t.Fatalf("error %q should direct the user from profile_dir to profile_name", err)
	}

	if err := validateBrowserConfig(types.BrowserConfig{Backend: "chromedp", ProfileDir: "/tmp/chrome-profile"}); err != nil {
		t.Fatalf("Chromedp backend rejected its profile_dir: %v", err)
	}
}

func TestCuaProfileNameDefaultsAndValidates(t *testing.T) {
	valid := []struct {
		name string
		in   string
		want string
	}{
		{name: "default", want: "nib"},
		{name: "letters digits dash underscore", in: "Nib_19-ci", want: "Nib_19-ci"},
		{name: "maximum length", in: strings.Repeat("a", 64), want: strings.Repeat("a", 64)},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cuaProfileName(types.BrowserConfig{ProfileName: tt.in})
			if err != nil {
				t.Fatalf("cuaProfileName(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("cuaProfileName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}

	invalid := []string{
		" leading-space",
		"trailing-space ",
		"has.dot",
		"has/slash",
		"café",
		strings.Repeat("a", 65),
	}
	for _, in := range invalid {
		t.Run("reject_"+in, func(t *testing.T) {
			if _, err := cuaProfileName(types.BrowserConfig{ProfileName: in}); err == nil {
				t.Fatalf("cuaProfileName(%q) succeeded, want an error", in)
			}
		})
	}

	if err := validateBrowserConfig(types.BrowserConfig{Backend: "cua", ProfileName: "has.dot"}); err == nil {
		t.Fatal("validateBrowserConfig accepted an invalid Cua profile name")
	}
}
