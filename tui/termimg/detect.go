package termimg

import (
	"os"
	"strings"
)

// Protocol identifies a terminal graphics protocol supported by the
// active terminal emulator.
type Protocol int

const (
	ProtocolNone Protocol = iota
	ProtocolKitty
	ProtocolITerm2
)

func (p Protocol) String() string {
	switch p {
	case ProtocolKitty:
		return "kitty"
	case ProtocolITerm2:
		return "iterm2"
	default:
		return "none"
	}
}

// Detect returns the terminal graphics protocol to use, based on
// environment variables. No DA1/DA2 queries are issued — only env vars
// are inspected, so detection never blocks on a pipe that doesn't
// respond.
//
// The NIB_IMAGE_PROTOCOL environment variable overrides detection when
// set to "kitty", "iterm2", or "off".
func Detect() Protocol {
	if override := os.Getenv("NIB_IMAGE_PROTOCOL"); override != "" {
		switch strings.ToLower(override) {
		case "kitty":
			return ProtocolKitty
		case "iterm2":
			return ProtocolITerm2
		case "off", "none", "disabled":
			return ProtocolNone
		}
	}

	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return ProtocolKitty
	}
	if os.Getenv("GHOSTTY_RESOURCES_DIR") != "" {
		return ProtocolKitty
	}
	if os.Getenv("ITERM_SESSION_ID") != "" {
		return ProtocolITerm2
	}
	if os.Getenv("WEZTERM_PANE") != "" {
		return ProtocolKitty
	}
	if tp := os.Getenv("TERM_PROGRAM"); tp == "WezTerm" || tp == "kitty" || tp == "ghostty" {
		return ProtocolKitty
	}
	return ProtocolNone
}
