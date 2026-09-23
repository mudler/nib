package types

import "testing"

func TestParseApprovalMode(t *testing.T) {
	for _, in := range []string{"prompt", "strict", "allowlist", "classify", "auto", "AUTO", " Classify "} {
		m, ok := ParseApprovalMode(in)
		if !ok || !m.Valid() {
			t.Errorf("%q: %q, %v", in, m, ok)
		}
	}
	if m, ok := ParseApprovalMode(""); !ok || m != ApprovalPrompt {
		t.Errorf("empty: %q, %v; want prompt", m, ok)
	}
	if _, ok := ParseApprovalMode("sometimes"); ok {
		t.Error("unknown mode accepted")
	}
}

func TestApprovalModeOrDefault(t *testing.T) {
	if ApprovalMode("").OrDefault() != ApprovalPrompt || ApprovalStrict.OrDefault() != ApprovalStrict {
		t.Fatal("OrDefault")
	}
}

func TestApprovalModeNames(t *testing.T) {
	got := ApprovalModeNames()
	want := []string{"prompt", "strict", "allowlist", "classify", "auto"}
	if len(got) != len(want) {
		t.Fatalf("names = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names = %v, want %v", got, want)
		}
	}
}
