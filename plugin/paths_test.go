package plugin

import "testing"

func TestBaseDirInPrefersOverride(t *testing.T) {
	if got := BaseDirIn("/custom/root"); got != "/custom/root" {
		t.Fatalf("BaseDirIn = %q, want /custom/root", got)
	}
}

func TestBaseDirInFallsBackToDefault(t *testing.T) {
	if got, want := BaseDirIn(""), BaseDir(); got != want {
		t.Fatalf("BaseDirIn(\"\") = %q, want %q", got, want)
	}
}
