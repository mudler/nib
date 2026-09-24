package termimg

import (
	"os"
	"testing"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Protocol
	}{
		{
			name: "no env vars → none",
			env:  map[string]string{},
			want: ProtocolNone,
		},
		{
			name: "KITTY_WINDOW_ID → kitty",
			env:  map[string]string{"KITTY_WINDOW_ID": "1"},
			want: ProtocolKitty,
		},
		{
			name: "GHOSTTY_RESOURCES_DIR → kitty",
			env:  map[string]string{"GHOSTTY_RESOURCES_DIR": "/foo"},
			want: ProtocolKitty,
		},
		{
			name: "ITERM_SESSION_ID → iterm2",
			env:  map[string]string{"ITERM_SESSION_ID": "x"},
			want: ProtocolITerm2,
		},
		{
			name: "WEZTERM_PANE → kitty",
			env:  map[string]string{"WEZTERM_PANE": "1"},
			want: ProtocolKitty,
		},
		{
			name: "TERM_PROGRAM=WezTerm → kitty",
			env:  map[string]string{"TERM_PROGRAM": "WezTerm"},
			want: ProtocolKitty,
		},
		{
			name: "override kitty",
			env:  map[string]string{"NIB_IMAGE_PROTOCOL": "kitty", "ITERM_SESSION_ID": "x"},
			want: ProtocolKitty,
		},
		{
			name: "override iterm2",
			env:  map[string]string{"NIB_IMAGE_PROTOCOL": "iterm2", "KITTY_WINDOW_ID": "1"},
			want: ProtocolITerm2,
		},
		{
			name: "override off",
			env:  map[string]string{"NIB_IMAGE_PROTOCOL": "off", "KITTY_WINDOW_ID": "1"},
			want: ProtocolNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all relevant env vars.
			for _, k := range []string{"KITTY_WINDOW_ID", "GHOSTTY_RESOURCES_DIR", "ITERM_SESSION_ID", "WEZTERM_PANE", "TERM_PROGRAM", "NIB_IMAGE_PROTOCOL"} {
				t.Setenv(k, "")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			got := Detect()
			if got != tt.want {
				t.Errorf("Detect() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestProtocolString(t *testing.T) {
	if ProtocolKitty.String() != "kitty" {
		t.Errorf("ProtocolKitty.String() = %q, want %q", ProtocolKitty.String(), "kitty")
	}
	if ProtocolITerm2.String() != "iterm2" {
		t.Errorf("ProtocolITerm2.String() = %q, want %q", ProtocolITerm2.String(), "iterm2")
	}
	if ProtocolNone.String() != "none" {
		t.Errorf("ProtocolNone.String() = %q, want %q", ProtocolNone.String(), "none")
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
